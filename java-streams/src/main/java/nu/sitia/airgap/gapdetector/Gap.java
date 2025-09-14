package nu.sitia.airgap.gapdetector;
import java.io.Serializable;
import java.util.Objects;

public class Gap implements Serializable {
    /** Start value of this gap */
    private long from;

    /** End value of this gap */
    private long to;

    @Override
    public boolean equals(Object o) {
        if (this == o) return true;
        if (o == null || getClass() != o.getClass()) return false;
        Gap gap = (Gap) o;
        return from == gap.from && to == gap.to;
    }

    @Override
    public int hashCode() {
        return Objects.hash(from, to);
    }

    /**
     * Constructor
     * @param from From value
     * @param to To value
     */
    public Gap(long from, long to) {
        this.from = from;
        this.to = to;
    }

    public long getFrom() {
        return from;
    }

    public void setFrom(long from) {
        this.from = from;
    }

    public long getTo() {
        return to;
    }

    public void setTo(long to) {
        this.to = to;
    }

    public String toString() {
        return from + "-" + to;
    }
}
