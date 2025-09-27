# Resend
Even if we use several sending threads and streams over multiple diodes, eventually some events will be marked as missing. Missing events will be written to the topic defined by PartitionDedupApp's GAP_TOPIC. From that topic, we can extract the information needed to resend all missing events.

## Resend Bundle and the applications to resend events

A resend of events consists of three steps:
1. Create a resend bundle (JSON file) with `createResourceBundle` from the downstream Kafka.
2. Move or copy that bundle to a machine with network access to the upstream Kafka.
3. Run the `resendBundle` app with the name and location of the resource bundle as a parameter to read the missing events from Kafka and send them to the receiver with UDP once again.

When all events have been resent, the gaps downstream should disappear, or at least decrease. You can check by inspecting the GAP_TOPIC or with Jolokia.

### createResourceBundle

This application will get all data from the GAP_TOPIC topic and create a JSON file with the topic name upstream, all partitions, and their respective missing offsets. In order for this to work, we must make sure the GAP_TOPIC only stores one version of our gaps, so we always get the latest one.

If the GAP_TOPIC topic has the property `cleanup.policy=compact`, then only the latest record for each key will be retained after compaction. Newer records with the same key will overwrite older ones.

To check the property value:
```sh
kafka-configs.sh --bootstrap-server <broker> --entity-type topics --entity-name gaps --describe
```

To add the property to an existing topic:
```sh
kafka-configs.sh --bootstrap-server <broker> --entity-type topics --entity-name <topic-name> --alter --add-config cleanup.policy=compact
```

You can also remove and recreate the gaps topic
```sh
kafka-topics.sh --delete --topic <topic-name> --bootstrap-server <broker>

kafka-topics.sh --create --topic <topic-name> --bootstrap-server <broker> --partitions 5 --config cleanup.policy=compact
```
The application `createResourceBundle` takes the following parameters:
* topic={GAP_TOPIC}
* bootstrap-servers={BOOTSTRAP_SERVERS}

Example:
```sh
./createResourceBundle topic=gaps bootstrap-servers=localhost:9092
```
The output will be written to stdout, so if you want to save the result to a file, you can use:
```sh
./createResourceBundle topic=gaps bootstrap-servers=localhost:9092 > filename.json
```
After creating the JSON file, inspect it to ensure it contains valid JSON.


Now, copy or move the file to the upstream network. Any computer upstream that has access to the diode and the Kafka cluster will suffice, but in most cases the best choice will be the same machine where the upstream service is installed.


Here, we will run another application that will read and parse the JSON file and query the Kafka cluster for the missing events. Then the events will be sent, just as the upstream application does. Collisions should not be a problem, since most organization uses switches. Since the upstream side of the diode will eventually send the events without collisions, the downstream side will also receive the same events.

### resendBundle
The `resendBundle` application takes similar parameters as the `createResourceBundle` application:
* topic={GAP_TOPIC}
* bootstrap-servers={BOOTSTRAP_SERVERS}

It will also require:
* bundle={filename.json}

When started, the `resendBundle` will read and emit the events as fast as possible, without throttling. When all events have been delivered, the application terminates.