# Redundancy and Load Balancing
air-gap is created with both redundancy and load balancing in mind. If you have a Kafka cluster on both ends of the diode, the transfer software should also be able to run with high availability.

## Redundancy
The most simple case of redundancy is if you have two streams of data from Kafka upstream to Kafka downstream.


![air-gap redundancy](img/air-gap%20reduncancy.png)

Here, we configure both air-gap upstreams to read the same topic: "input". They both send to a dedicated downstream. Since Kafka only allows one client at a time to write to a partition and we use partitions for the deduplication process, both downstream can't write to the same topic. The solution is that they write to different topics: "transfer1" and "transfer2". This is achieved by adding the property 

The downstream receivers can set the property: topicTranslations to 
```
topicTranslations={"input": "transfer1"}
```
and
```
topicTranslations={"input": "transfer2"}
```
respectively. Now, all data for the input topic upstreams will be duplicated in the topics: transfer1 and transfer2. The next step is to configure the deduplicator. The one step that is different than a single stream setup is the RAW_TOPICS field. Normally, we set that to the name of the topic we should read. Now, we set it to a list of topics to read:
```
RAW_TOPICS=transfer1,transfer2
```

If the input topic has $n$ partitions, then both the transfer1 and transfer2 partitions need to have $n$ partitions too.

**Note:** If your events use GUIDs or other non-partitioned keys (i.e., keys that do not follow the `topic_partition_offset` scheme), these events are still supported. They will be delivered and deduplicated, but are not tied to a specific partition or deduplication instance. Instead, they are distributed among the available instances based on their key value. This allows mixed key types in the same deployment.

## Load balancing
There is a filter option in the upstream application so you can choose to just send some events to the UDP receiver. This can be used in a similar setup as the redundancy scheme above:

![air-gap load balancing](img/air-gap%20reduncancy.png)

### Filters
First we need to look at filters. Filters are a mechanism in upstream that enable the upstream application to filter out events that do not adhere to a numbering scheme. The scheme is described in three groups of one or more numbers.

If we want to send just odd numbers, we will use the filter: `1,3,5`. Here we have three groups: `1`, `2` and `3`. The first two are actually enough to write the filter so the third group is used to check that the numbers in that group will be delivered.

A more complex example can be that we want to deliver just 20% of the events. We can do this in many ways but this is one:
```
Environment="AIRGAP_UPSTREAM_DELIVER_FILTER=0,5,10" 
```
Now, offsets that ends with a 0 or 5 will be delivered, no other.

The groups can contain more than one number too. Say we want to deliver everything but events ending in 0 or 5:
```
Environment="AIRGAP_UPSTREAM_DELIVER_FILTER=1,2,3,4,6,7,8,9,11,12,13,14" 
```
Here, the groups are `1,2,3,4`, `6,7,8,9` and the control group: `11,12,13,14`. 

Now, we are ready to write the load balancing configuration

### Load balancing filter

The setup looks the same, but we configure upstream1 to filter:
```
Environment="AIRGAP_UPSTREAM_DELIVER_FILTER=1,3,5" 
```
and the upstream2 to filter:
```
Environment="AIRGAP_UPSTREAM_DELIVER_FILTER=2,4,6" 
```
Now, upstream1 will only send odd events from each partition and upstream2 will only send even events. The downstream configuration can be the same as the Redundancy example above, since both downstream will receive events for all the partitions, but only half of the events that are in the upstream input topic.

## Redundancy and Load Balancing at the same time
If we combine the methods above, we can adjust the level of redundancy and load balancing at the same time. In the next example, we send all data over three diodes twice. If one node cease to function, the other two will still work and send half the data once and half the data twice over two diodes.

![air-gap redundancy load balancing](img/air-gap%20reduncancy-loadbalancing.png)

Here, we configure the upstream filters to:
```
Environment="AIRGAP_UPSTREAM_DELIVER_FILTER=1,2,4,5,7,8"
Environment="AIRGAP_UPSTREAM_DELIVER_FILTER=2,3,5,6,8,9"
Environment="AIRGAP_UPSTREAM_DELIVER_FILTER=3,4,6,7,9,10"
```
Here, each event will be delivered by two upstreams, so only two needs to be running at the same time. Also, each node only need to send 2/3 of the data. You can now configure hardware and air-gap to achieve your prefered level of redundancy and load balancing at the same time.