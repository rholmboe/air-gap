package nu.sitia.airgap.gapdetector;

import org.junit.jupiter.api.Test;
import java.util.List;
import static org.junit.jupiter.api.Assertions.*;

public class GapDetectorTest {
    @Test
    public void testNoGapsWhenSequential() {
        GapDetector gd = new GapDetector(10, 2);
        for (long i = 0; i < 10; i++) {
            boolean dup = gd.check(i, null);
            assertFalse(dup, "Should not be duplicate for new offset " + i);
        }
        assertTrue(gd.getAllGaps().isEmpty(), "No gaps expected for sequential input");
    }

    @Test
    public void testDetectSingleGap() {
        GapDetector gd = new GapDetector(10, 2);
        gd.check(0, null);
        gd.check(2, null);
        List<Gap> gaps = gd.getAllGaps();
        assertEquals(1, gaps.size(), "Should detect one gap");
        Gap gap = gaps.get(0);
        assertEquals(1, gap.getFrom());
        assertEquals(1, gap.getTo());
    }

    @Test
    public void testDetectMultipleGaps() {
        GapDetector gd = new GapDetector(10, 2);
        gd.check(0, null);
        gd.check(2, null);
        gd.check(4, null);
        List<Gap> gaps = gd.getAllGaps();
        assertEquals(2, gaps.size(), "Should detect two gaps");
        assertEquals(1, gaps.get(0).getFrom());
        assertEquals(1, gaps.get(0).getTo());
        assertEquals(3, gaps.get(1).getFrom());
        assertEquals(3, gaps.get(1).getTo());
    }

    @Test
    public void testDuplicateDetection() {
        GapDetector gd = new GapDetector(10, 2);
        assertFalse(gd.check(0, null));
        assertTrue(gd.check(0, null), "Second check for same offset should be duplicate");
    }

    @Test
    public void testGapClosedByLateArrival() {
        GapDetector gd = new GapDetector(10, 2);
        gd.check(0, null);
        gd.check(2, null);
        assertEquals(1, gd.getAllGaps().size());
        gd.check(1, null); // fill the gap
        assertTrue(gd.getAllGaps().isEmpty(), "Gap should be closed after late arrival");
    }

    @Test
    public void testWindowPurgeCallback() {
        GapDetector gd = new GapDetector(1, 2); // small window for easy purging
        final boolean[] purged = {false};
        gd.check(0, w -> purged[0] = true);
        gd.check(1, w -> purged[0] = true);
        gd.check(2, w -> purged[0] = true); // triggers purge of first window
        assertTrue(purged[0], "Purge callback should be called");
    }
}
