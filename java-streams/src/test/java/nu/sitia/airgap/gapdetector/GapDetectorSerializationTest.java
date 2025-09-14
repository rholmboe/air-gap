package nu.sitia.airgap.gapdetector;

import org.junit.jupiter.api.Test;
import java.io.*;

import static org.junit.jupiter.api.Assertions.*;

class GapDetectorSerializationTest {

    @Test
    void testSerializationAndDeserialization() throws Exception {
        GapDetector original = new GapDetector(1000, 5);
        // Simulate some activity
        original.check(1, null);
        original.check(2, null);
        original.check(1001, null);
        original.check(2001, null);

        // Serialize
        ByteArrayOutputStream bos = new ByteArrayOutputStream();
        ObjectOutputStream out = new ObjectOutputStream(bos);
        out.writeObject(original);
        out.close();
        byte[] bytes = bos.toByteArray();

        // Deserialize
        ByteArrayInputStream bis = new ByteArrayInputStream(bytes);
        ObjectInputStream in = new ObjectInputStream(bis);
        GapDetector restored = (GapDetector) in.readObject();

        // Assertions
        assertEquals(original, restored); // if equals is implemented
        assertFalse(original == restored); // different instances
    }
}