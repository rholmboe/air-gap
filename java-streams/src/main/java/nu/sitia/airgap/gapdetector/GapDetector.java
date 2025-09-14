package nu.sitia.airgap.gapdetector;

import org.roaringbitmap.longlong.Roaring64NavigableMap;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.util.*;
import java.util.function.Consumer;
import java.util.stream.Collectors;

public class GapDetector implements java.io.Serializable {
    /**
     * Estimate the memory usage (in bytes) for all loaded windows.
     * This is a rough estimate: each window has a small overhead, and each offset in the bitmap costs memory.
     * RoaringBitmap is compact, but we estimate 16 bytes per window (object overhead) and 16 bytes per offset (bitmap + object refs).
     */
    public long estimateMemoryBytes() {
        long total = 0;
        for (Window w : windows) {
            // Estimate: 16 bytes for Window object, 16 bytes per offset in bitmap
            long cardinality = w.received.getLongCardinality();
            total += 16; // window object overhead
            total += 16 * cardinality; // each offset in bitmap
        }
        return total;
    }
    private static final Logger LOG = LoggerFactory.getLogger(GapDetector.class);
    private static final long serialVersionUID = 1L;

    @Override
    public boolean equals(Object o) {
        if (this == o) return true;
        if (o == null || getClass() != o.getClass()) return false;
        GapDetector that = (GapDetector) o;
        return windowSize == that.windowSize &&
                maxWindows == that.maxWindows; //&&
                //Objects.equals(windows, that.windows);
    }

    @Override
    public int hashCode() {
        return Objects.hash(windowSize, maxWindows, windows);
    }

    /** Track the highest offset ever received */
    private long lastReceived = Long.MIN_VALUE;
    /** Track the lowest offset ever received */
    private long minReceived = Long.MAX_VALUE;

    private final long windowSize; // max offsets per window
    private int maxWindows;  // max number of windows to keep
    private final LinkedList<Window> windows = new LinkedList<>();

    public GapDetector(long windowSize, int maxWindows) {
        this.windowSize = windowSize;
        this.maxWindows = maxWindows;
    }

    public long getLastReceived() {
        return lastReceived;
    }

    public long getMinReceived() {
        return minReceived;
    }

    // Expose a public view for purged windows
    public interface WindowView {
        long getMinOffset();
        long getMaxOffset();
        long getCreatedAt();
        List<Gap> findGaps();
    }

    /**
     * Mark a number as received. Optionally emit missing offsets from purged windows.
     *
     * @param number  the offset received
     * @param onPurge callback invoked before a window is purged, to process missing offsets
     * @return true if this number was already received, false if it’s newly received
     */
    public boolean check(long number, Consumer<WindowView> onPurge) throws OutOfMemoryError {
        if (number > lastReceived) {
            lastReceived = number;
        }

        Window w = findWindow(number);
        if (w == null) {
            // special case: If the number is less than the lowest known offset,
            // then we deliver it as unseen, but do not create a new window
            if (!windows.isEmpty() && number < windows.getFirst().minOffset) {
                return false;
            }

            // purge oldest if exceeding maxWindows
            if (windows.size() >= maxWindows) {
                Window old = windows.removeFirst();
                if (onPurge != null) {
                    onPurge.accept(new WindowView() {
                        public long getMinOffset() { return old.minOffset; }
                        public long getMaxOffset() { return old.maxOffset; }
                        public long getCreatedAt() { return old.createdAt; }
                        public List<Gap> findGaps() { return old.findGapsFiltered(minReceived); }
                    });
                }
            }
            long start = number - (number % windowSize);
            long end = start + windowSize - 1;
            try {
                w = new Window(start, end);
                windows.add(w);
            } catch (OutOfMemoryError oom) {
                LOG.error("[GapDetector] OutOfMemoryError: Failed to allocate new window for offsets " + start + "-" + end + ": " + oom);
                // Prevent further window growth
                this.maxWindows = windows.size();
                LOG.error("[GapDetector] Reducing maxWindows to " + this.maxWindows);
                // Retry once (guard against infinite recursion)
                if (windows.size() == maxWindows) {
                    return check(number, onPurge);
                }
                throw new OutOfMemoryError("Failed to allocate new window for offsets " + start + "-" + end + ": " + oom);
            }
        }

        boolean alreadyReceived = w.received.contains(number - w.minOffset);
        w.markReceived(number);
        // Only update minReceived if this number was actually marked as received
        if (!alreadyReceived && number < minReceived) {
            minReceived = number;
        }
        return alreadyReceived;
    }

    private Window findWindow(long number) {
        for (Window w : windows) {
            if (w.contains(number)) return w;
        }
        return null;
    }

    /** Emit all gaps in windows that were created before a given timestamp */
    public List<Gap> emitGapsBefore(long timestamp) {
        List<Gap> gaps = new ArrayList<>();
        for (Window w : windows) {
            if (w.createdAt < timestamp) {
                gaps.addAll(w.findGapsFiltered(minReceived));
            }
        }
        return gaps;
    }

    /** Extract all missing offsets from windows, filtering out gaps below minReceived. */
    public List<Gap> getAllGaps() {
        List<Gap> gaps = new ArrayList<>();
        for (Window w : windows) {
            for (Gap g : w.findGapsFiltered(minReceived)) {
                if (lastReceived >= g.getFrom()) {
                    gaps.add(g);
                }
            }
        }
        return gaps;
    }

    public void purge() {
        windows.clear();
        minReceived = Long.MAX_VALUE;
    }

    /** Purge all windows that are full and contain no gaps */
    public void purgeFullWindows() {
        windows.removeIf(Window::isFull);
    }

    /** Represents a single window */
    private static class Window implements java.io.Serializable {
        final long minOffset;
        final long maxOffset;
        final long createdAt;
        final Roaring64NavigableMap received = new Roaring64NavigableMap();

        Window(long minOffset, long maxOffset) {
            this.minOffset = minOffset;
            this.maxOffset = maxOffset;
            this.createdAt = System.currentTimeMillis();
        }

        boolean contains(long number) {
            return number >= minOffset && number <= maxOffset;
        }

        void markReceived(long number) {
            received.add(number - minOffset);
        }

        /**
         * Find gaps, but only return those where gap.from >= minAllowed.
         */
        List<Gap> findGapsFiltered(long minAllowed) {
            List<Gap> gaps = new ArrayList<>();

            long lastChecked = 0;
            long windowLength = maxOffset - minOffset + 1;

            // Iterate over present runs and derive gaps
            org.roaringbitmap.longlong.LongIterator it = received.getLongIterator();
            long prev = -1;

            while (it.hasNext()) {
                long val = it.next();
                if (prev == -1 && val > 0) {
                    // Gap at the beginning
                    long gapFrom = minOffset;
                    long gapTo = minOffset + val - 1;
                    if (gapFrom >= minAllowed) {
                        gaps.add(new Gap(gapFrom, gapTo));
                    } else if (gapTo >= minAllowed) {
                        gaps.add(new Gap(minAllowed, gapTo));
                    }
                } else if (prev != -1 && val > prev + 1) {
                    // Gap between prev and val
                    long gapFrom = minOffset + prev + 1;
                    long gapTo = minOffset + val - 1;
                    if (gapFrom >= minAllowed) {
                        gaps.add(new Gap(gapFrom, gapTo));
                    } else if (gapTo >= minAllowed) {
                        gaps.add(new Gap(minAllowed, gapTo));
                    }
                }
                prev = val;
                lastChecked = val;
            }

            // Tail gap if not filled up to the maxOffset
            if (prev != -1 && lastChecked < windowLength - 1) {
                long gapFrom = minOffset + lastChecked + 1;
                long gapTo = maxOffset;
                if (gapFrom >= minAllowed) {
                    gaps.add(new Gap(gapFrom, gapTo));
                } else if (gapTo >= minAllowed) {
                    gaps.add(new Gap(minAllowed, gapTo));
                }
            } else if (prev == -1) {
                // Special case: window is completely empty
                long gapFrom = minOffset;
                long gapTo = maxOffset;
                if (gapFrom >= minAllowed) {
                    gaps.add(new Gap(gapFrom, gapTo));
                } else if (gapTo >= minAllowed) {
                    gaps.add(new Gap(minAllowed, gapTo));
                }
            }

            return gaps;
        }

        boolean isFull() {
            return received.getLongCardinality() == (maxOffset - minOffset + 1);
        }
    }

    /** Optional: expose all windows for external inspection */
    public List<Map<String, Number>> listWindows() {
        return windows.stream()
            .map(w -> Map.of(
                "minOffset", (Number) w.minOffset,
                "maxOffset", (Number) w.maxOffset,
                "createdAt", (Number) w.createdAt,
                "receivedCardinality", (Number) w.received.getLongCardinality(),
                "lastReceived", (Number) lastReceived
            ))
            .collect(Collectors.toList());
    }


}