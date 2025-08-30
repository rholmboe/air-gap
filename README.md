## air-gap Kafka to Kafka Topic Transfer over UDP with Guaranteed Delivery
This project aims to solve the problem of transferring events from a Kafka topic in near real time over an unsecure UDP connection with guaranteed once-only delivery of the events. This can be useful, e.g., for transmitting log events over a hardware diode. The information leaving the sending side may be encrypted with symmetric keys and the key exchange is automatic with use of public key encryption. 

In Kafka, events may have a key and a value. The key part of events upstreams will be lost in the transmission, since the key part downstream is used for message identification and thus detection of lost messages (gap-detection). 

For resend of lost events that gap-detection identifies, a back channel must be present but can be a manual routine with input from keyboard or from a file transfer. The information is mainly what topic, partition and id to start reading from. When that information is present upstream, you need to run one application that configures upstream to read from a new timestamp (set-timestamp) and then restart upstream to read from the new timestamp. 

## Notation
There are four executable files that together constitutes the transfer software.
- Upstream - on the sending side of the diode, also used as the name of the program that consumes Kafka events and produces UDP packets
- Downstream - on the receiving side of the diode, also used as the name of the program that receives UDP packets and produces Kafka events for the receiving Kafka
- gap-detector - The program that consumes the freshly written Kafka events from Downstream and looks for missing events. This will also deduplicate the events, so if an event is received more than once downstream, only the first of these will be delivered
- set-timestamp - This progam consumes information from the gap-detector and instructs the sending upstream process to restart at an earlier point in time in the upstream Kafka event stream so the lost events may be sent again

To use guaranteed delivery, you must be able to copy information from the gap-detector that runs downstream to the set-timestamp program that runs upstream. The information that needs to traverse the diode in the wrong direction is basically a topic id, partition id and position for the first lost event. set-timestamp is an application that uses that information, queries Kafka upstream on the last timestamp before that event and configures the upstream application to start reading at that timestamp.

```
                 Manually bridge the air-gap with topic_partition_position information
Kafka Upstream   <----------------------------------------------+
    ^                                                           |
    upstream.go > (UDP) > downstream.go > Kafka Downstream > Gap report
                                            v       ^
                                           gap-detector
                                               v
                                        File: gap state 
                                    
```
N.B., neither the configuration files nor the key files are not shown in the above image.

upstream reads from a Kafka topic specified in the upstream configuration file. It then enrypts the data and sends it over the UDP connection, that might include a hardware diode. Packets that are more than MTU in size (with header) will get fragmented and sent as several UDP packets. Downstream listens to UDP packets, performs defragmentation and decryption of the content and writes to a Kafka instance and topic specified in the downstream configuration file.

Downstream Kafka contains two topics: one that downstream.go writes to and one that gap-detector.go writes to. The first may contain duplicates but the one from gap-detector should very rarely contain duplicates. Consume the gap-detector output topic, and send to your SIEM of choice. The data sent from Kafka upstream to Kafka downstream is treated as bytes and not strings, so any string encoding in upstream should be present in the final Kafka topic. It should be able to correctly send files and images to the downstream Kafka but that is still to be tested. 

## Getting started
### Very simple use case
To enable users to get started without Kafka and without hardware diode, use the following properties files:
- upstream3.properties
- downstream3.properties

These properties files are configured for getting a few random strings instead of reading from Kafka and to send with UDP without encyption. Change the targetIP in upstream3.properties to the one you would like to send to, and change the targetIP in downstream3.properties to the same value. The IP address must be one that downstrem can bind to and that upstream can send to.

In one terminal, start the server with:
```
go run src/downstream/downstream.go config/downstream3.properties
```

In a new terminal, start the client (sender) with:
```
go run src/upstream/upstream.go config/upstream3.properties
```
A few messages should now be sent from upstream and received by downstream. From here, add encryption and connections to Kafka to enable all features.

## Principle of Transmission
### Upstream
Upstream has two main purposes, to read events from the upstream Kafka, and to send the events to the UDP receiver. Since the UDP transmission may be insecure, we may encrypt the events using symmetric AES256 in GCM mode. The key is generated on startup, and at configurable intervals. The key is also sent over the UDP transmission to the receiver, encrypted with the receiver's public key, that we store in a file upstream. To change the public key, it should suffice to add a new key and change the configuration file to point to the new key. When the next key generation is set, the new key will be used instead of the old.

The encryption is only enabled if the `publicKeyFile` is et to the downstream public certificate pem file.

### Downstream
Downstrem should receive UDP data (encrypted and unencrypted) and write events to the downstream Kafka on a configured topic. When upstream starts, cleartext events will be generated, as well as an encrypted key. The cleartext messages should be forwarded to the downstream Kafka, key exchange messages should be handled internally and encrypted messages should be decrypted and then forwarded to the downstream Kafka.

On downstream startup, a file glob is read from the configuration file. When a key exchange message is received, the file glob is used to load all private key files that matches the glob. The encrypted message is decrypted with all the private keys until one succeeds. To know if an encryption was successful or not, the symmetric key that upstream sends is prepended with a known string before encryption, a so called guard. When the decrypted message starts with the guard, the rest is used as the new symmetric key. Downstream writes the events to the downstream Kafka with the upstream Kafka topic name, partition id and position as the downstream key. The name of the downstream Kafka topic downstream writes to is configurable. That topic is then consumed by gap-detection.

#### Performance
UDP receive is normally faster than Kafka writes. The downstream application tries to safeguard against lost packets by using a lightweight thread that receives events, decrypts them using AES256 (quite fast) and then adds the events to an array. Another thread consumes the array and writes to Kafka using the async protocol (that also returns immediately and processes the write in another thread). If the performance is not enough, first try to add nodes to the Kafka cluster and add the nodes to the bootstrapServers configuration in the downstream process. You can also try to add several events together before writing them to the upstream Kafka, since there is some overhead for each Kafka event, especially for writing. As a last resort, the upstream sender can be set to throttle (no code for that yet), e.g., by adding a small time.Sleep after each sent event. You should be able to securely transmit tens of thousand events every second using one transmission chain, but for large installations you might have to add more sender/receiver chains, as well as upgrade the Kafka instances.

### Automatic resend
Since UDP is an unreliable protocol, you can set up air-gap to automatically resend logs at specific time intervals. In the upstream property file, add the following property:
`sendingThreads=[{"Name": seconds-delay}, ... ]`

Example:
```
groupID=testGroup
sendingThreads=[{"now": 0}, {"3minutes-ago": -180}]
```
For each name-dealy, a thread will be created in upstream. Each thread connects to Kafka with a group name that consists of the groupID from the property file, a "-" character, and the name from the sendingThreads property. In the example above, two threads will be created. One named "now" with 0 seconds delay and one named "3minutes-ago" with 180 seconds delay.

The thread with name "now" will connecto to Kafka with a group id of "testGroup-now" and the other thread "testGroup-3minutes-ago".

When a thread reads a message in Kafka, it will check if the Kafka timestamp - the delay (delay is a negative number) is at least equal to, or greater than, the current time. If not, it will sleep until the time is right to send.

If a message is read but not delivered (because the thread is sleeping) and the application terminates, then the

### Gap Detection
Since UDP diodes only allow traffic in one direction, we need to invent a new feedback loop in case any events are not successfully delivered over the connection. We do this by enumerating all events we get from the upstream Kafka, send them over the UDP connection and use the enumeration as a key for the events in the downstream Kafka.

Now, the gap-detector will process each event in the downstream Kafka that the downstream UDP receiver wrote to, using the provided key from the upstream Kafka. Let's denote the events {key, info}. The key will be the concatenation of the upstream Kafka Topic name, an underscore _, the upstream Kafka partition id, another underscore _ and lastly the position of the event in that partition. This also means we will not be able to transmit the upstrema Kafka key to the downstream Kafka. The gap-detector will now store positions for all received partitions along with a list of gaps, that are from-to positions that we miss in a specific partition. All processed events from the gap-detector will be written to another topic in the downstream Kafka. The new topic will have no duplicates.

Regulary, the gap-detector will find the smallest position for each partition and emit an event that contains topicName_partitionId_position for each of those positions, e.g., topicname_0_34 topicname_1_42343543534 topicname_2_2435234. That list can be used in set-timestamp to instruct the upstream process to start at a new position. Also, this list will be sent on Ctrl-C or os signal shutdown.

If the gap-detection fails, we can still use LogGenerator to search for missing events in an index in Elasticsearch, but this method takes more processing power than gap-detector.

gap-detector loads the state (current gaps) from file on start and saves the state to the same file each time the sendGapsEverySeconds is triggered. It also saves the state when a terminating signal is detected.

#### Possible duplicates and missing events
If the gap-detector crashes or otherwise is brought down non-gracefully (out of disk etc), the unsaved data since last save will not be persisted. Next time the gap-detector is started, the Kafka position (which is stored in Kafka) will proceed on the next event but the state was saved earlier in the event history, so events between last gap save and the next startup will be delivered again to the output topic by gap-detector. There's also a chance that events are processed in memory during a crash and will not be delivered to the output topic by gap-detector. To counter this, there is the possibility to use Kafka transactions, so gap-detector reads one event from the input topic with a Kafka transaction and doesn't mark the event as handled until it gets a transaction message from the Kafka output topic saying the event was successfully persisted. 

Transactions will lower the performance of gap-detector, possibly by a substantial value. A middle way is to use the last saved gap state timestamp as a from parameter in gap-detector.
Every time the gap state is saved to disk, the gap-detector configuration file from parameter is updated to the timestamp of the last read event i Kafka. When the gap-detector restarts it will start to read from Kafta at the time from the configuration. Since the configuration now is in sync with the state, no events should be missed. All events that were delivered after the last sync (before the crash) will be deivered again, so here we won't avoid duplicates.

If it's essential to avoid duplicates, there are schemes for setting the primary key for events in, e.g., Elasticsearch. The process reading from the gap-detector output topic and use the key from the event as the primary key in Elasticsearch. This will totally avoid duplicates.

The configuration file and the gap state should be considered for backup, since the application might crash in the middle of writing the files.

### Using Gap Detection Events to Resend from Upstream
set-timestamp takes a configuration file and a list of, at least one, topicName_partitionId_position as arguments. For each topicName_partitionId_position it checks the upstream Kafka for the receive time of that specific events. When all events are processed, the earliest timestamp found is written to the from parameter in the supplied configuration file.

When set-timestamp has updated the configuration file, just restart the upstream process to start reading at that point in time instead of where it was in the event stream. The configuration file will be updated so that the from value is removed. This way, Kafka will remember where upstream was in the event stream when the application is restarted the next time.

## Keys
Generate keystore with certificate or obtain otherwise.
```bash
keytool -genkey -alias keyalias -keyalg RSA -validity 365 -keystore keystore.jks -storetype JKS
```

Export the java keystore to a PKCS12 keystore:
```bash
keytool -importkeystore  -srckeystore keystore.jks  -destkeystore keystore.p12  -deststoretype PKCS12 -srcalias keyalias
```

Export certificate using openssl:
```bash
openssl pkcs12 -in keystore.p12  -nokeys -out cert.pem
```

Export unencrypted private key:
```bash
openssl pkcs12 -in keystore.p12  -nodes -nocerts -out key.pem
```

Now, add the cert.pem to upstream (public key) and the
key.pem to downstream. Downstream can have several private keys,
if all of the filenames are covered by the same file glob

## Encryption
The provided solution encrypts data in transit over the UDP connection. If needed, add TLS and authentication to all the Kafka connections. If the event generation is also secured with Kafka TLS, then no part of the event chain needs to be in clear text.

For information about Kafka and TLS, see e.g., https://dev.to/davidsbond/golang-implementing-kafka-consumers-using-sarama-cluster-4fko

## Configuration
### Upstream
The upstream OS must be configured for static route and arp for the ip address used to send UDP. Upstream will need both a public key and a configuration file with the following properties:

```properties
id=Upstream_1
nic=en0
targetIP=127.0.0.1
targetPort=1234
bootstrapServers=192.168.153.138:9092
topic=transfer
groupID=test
# The from parameter will issue a new kafka client id
# and search from the beginning of the topic until the
# insert time for the event is at least this time, then
# start to emit the events. Handy for resending of events
# format for from=2024-01-28T10:24:55+01:00
from=
publicKeyFile=certs/server2.pem
# random will just create a lot of random characters to test e.g. performance
#source=random
source=kafka
# Every n seconds, generate a new symmetric key
generateNewSymmetricKeyEvery=500
# Read the MTU from the nic, or set manually
mtu=auto
```

All configuration can be overridden by environment variables. In the case a file is parsed that will be parsed first and may result in configuration errors. After that, any environment variables are checked and, if found, will overwrite the file configuration.

The environment variables are named as:
```bash
AIRGAP_UPSTREAM_{variable name in upper case}
```
Example:
```bash
export AIRGAP_UPSTREAM_ID=NEW-ID
export AIRGAP_UPSTREAM_NIC=ens0
export AIRGAP_UPSTREAM_TARGET_IP=255.255.255.255
...
```
 

Resend will receive a major overhaul so this section is now deprecated:

The same configuration file is used for set-timestamp. set-timestamp uses the bootstrapServers to query for timestamps for each topic partition and position in the set-timestamp arguments. When the earlierst timestamp has been retrieved, the configuration files's from parameter is set to that timestamp. When upstream restarts, it will read all Kafka events from the beginning and discard those before the from timestamp. During the start phase, set-timestamp will revert the from parameter to an empty string so the next startup will use Kafka's stored pointer for where to read from in the future. 

### Downstream
Downstream has a similar configuration file as upstream

```properties
id=Downstream_1
nic=en0
targetIP=127.0.0.1
targetPort=1234
bootstrapServers=192.168.153.138:9092
topic=log
privateKeyFiles=certs/private*.pem
target=kafka
verbose=true
mtu=auto
clientId=downstream
```

The property privateKeyFiles should point to one or more private key files that will be tried in decrypting key exchange events from upstream.

gap-detector uses it's own configuration file, where the from parameter is updated every time the gaps are saved to file.

```properties
id=Gap_Detector_1
bootstrapServers=192.168.153.138:9092
topic=log
topicOut=dedup
verbose=false
clientId=gap_detector
sendGapsEverySeconds=5
from=2024-01-30T12:54:15+01:00
gapSaveFile=tmp/gaps.json
```
### Logging
All code uses the log logging packets. To be able to write the most important logs to a file, a new variable, Logger, is introduced, that can accept a file name from the configuration. All code is not yet connected to that logging output so some logging will still write to stdout. Important events that can be delivered in the output of the code (event stream) are also sent into the event stream.

# Possible Problems
Some problems that may arise are:
- The UDP sending fails. Check that you have static arp (arp -s) and route (ip route add) enabled.
- If the UDP connection is very unstable, then the gap file (and memory footprint) may grow very large. Consider monitoring the gap file and alert if it grows too much
- If the UDP connection is very unstable, then condider using two instances of upstream/downstream sending the same information over two different hardwar diodes to the same Kafka and the same topic. Duplicates should be removed by the gap-detection so the result should be a more stable connection.

# Service
The applications responds to os signals and can be installed as a service in, e.g., Linux. 
See https://fabianlee.org/2022/10/29/golang-running-a-go-binary-as-a-systemd-service-on-ubuntu-22-04/

## Compile
There is a Makefile that will get the latest tag from git and save in version.go, then build upstream and downstream.
```bash
make            # builds both upstream and downstream
make upstream   # builds only upstream
make downstream # builds only downstream
make clean      # removes binaries and version.go
```
To build manually, change directory to the application you would like to build (./src/upstream, ...). 
Compile the applications with `go build {filename}`.

Example:
```bash
cd src/upstream
go build upstream.go
```

Now we have a compiled file called `upstream`. We can run the application with `./upstream`, but you will still need a configuration file.

To turn the application into a service we need to create a service file: `/lib/systemd/system/upstreamservice.service`
Change the paths to where you will install the service binary and comfiguration file

```properties
[Unit]
Description=Upstream Diode service
ConditionPathExists=/home/ubuntu/work/src/upstream/upstream
After=network.target
 
[Service]
Type=simple
User=upstream
Group=upstream
LimitNOFILE=1024

Restart=on-failure
RestartSec=10
startLimitIntervalSec=60

WorkingDirectory=/home/ubuntu/work/src/upstream
ExecStart=/home/ubuntu/work/src/upstream/upstream configfile.properties

# make sure log directory exists and owned by syslog
PermissionsStartOnly=true
ExecStartPre=/bin/mkdir -p /var/log/upstream
ExecStartPre=/bin/chown syslog:adm /var/log/upstream
ExecStartPre=/bin/chmod 755 /var/log/upstream
SyslogIdentifier=upstream
 
[Install]
WantedBy=multi-user.target
```

## Enable serice and start
```
sudo systemctl enable upstream.service
sudo systemctl start upstream
```

## Dependencies
air-gap uses IBM/sarama for the Kafka read/write. For other dependencies, check the go.mod file.

## License
See LICENSE file

# Release Notes

## 0.1.2-SNAPSHOT
* All configuration from files can be overridden by environment variables. See Configuration Upstream
* UDP sending have been made more robust
* Transfer of binary data from upstream to downstream is now supported
* Sending a sighup to upstream or downstream will now force a re-write of the log file, so you can rotate the log file and then sighup the application to make it log to a new file with the name specified in the upstream or downstream configuration.
* air-gap now supports TLS and mTLS to Kafka upstream and downstream. 

## 0.1.1-SNAPSHOT
### Several sending threads
air-gap now supports several sending threads that all have a specified time offset, so you can start one thread that consumes everything from Kafka as soon as it's available, one that inspects Kafka content that was added for an hour ago and so on. See Automatic resend above.