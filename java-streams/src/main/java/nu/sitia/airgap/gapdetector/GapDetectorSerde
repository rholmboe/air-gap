package nu.sitia.airgap.gapdetector;

import org.apache.kafka.common.serialization.Serde;
import org.apache.kafka.common.serialization.Serializer;
import org.apache.kafka.common.serialization.Deserializer;

import java.io.*;

public class GapDetectorSerde implements Serde<GapDetector> {
    @Override
    public Serializer<GapDetector> serializer() {
        return new Serializer<GapDetector>() {
            @Override
            public byte[] serialize(String topic, GapDetector data) {
                if (data == null) return null;
                try (ByteArrayOutputStream bos = new ByteArrayOutputStream();
                     ObjectOutputStream out = new ObjectOutputStream(bos)) {
                    out.writeObject(data);
                    return bos.toByteArray();
                } catch (IOException e) {
                    throw new RuntimeException(e);
                }
            }
        };
    }

    @Override
    public Deserializer<GapDetector> deserializer() {
        return new Deserializer<GapDetector>() {
            @Override
            public GapDetector deserialize(String topic, byte[] bytes) {
                if (bytes == null) return null;
                try (ByteArrayInputStream bis = new ByteArrayInputStream(bytes);
                     ObjectInputStream in = new ObjectInputStream(bis)) {
                    return (GapDetector) in.readObject();
                } catch (IOException | ClassNotFoundException e) {
                    throw new RuntimeException(e);
                }
            }
        };
    }
}