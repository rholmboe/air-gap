# Monitoring upstream, downstream and dedup with Metricbeat
In a production environment, jconsole is not availabe if you run the deduplicator as a service. You can collect metrics with the following guide, where only the system metrics are available for the upstream and downstream but the deduplicator exports some attributes to JMX and can be inspected with Jolokia.

## 1. Install Metricbeat
Follow the [official Elastic documentation](https://www.elastic.co/guide/en/beats/metricbeat/current/metricbeat-installation.html) for your OS, or on Fedora/RHEL:
```sh
sudo rpm --import https://artifacts.elastic.co/GPG-KEY-elasticsearch
cat <<EOF | sudo tee /etc/yum.repos.d/elastic.repo
[elastic-8.x]
name=Elastic repository for 8.x packages
baseurl=https://artifacts.elastic.co/packages/8.x/yum
gpgcheck=1
gpgkey=https://artifacts.elastic.co/GPG-KEY-elasticsearch
enabled=1
autorefresh=1
type=rpm-md
EOF
sudo dnf install metricbeat
```

## 2. Enable Metricbeat Modules

### System Metrics (CPU, memory, etc.)
```sh
sudo metricbeat modules enable system
```

### Deduplicator (JMX) Metrics via Jolokia
The deduplicator already exposes JMX methods to JConsole for monitoring (you can also purge the gaps). Those methods are also available to monitor with Jolokia.

1. **Download Jolokia agent:**
   - [Jolokia Releases](https://jolokia.org/download.html)
2. **Change the .service file to start the Java app with Jolokia agent:**
   ```sh
   java -javaagent:/path/to/jolokia-agent.jar=port=8778,host=127.0.0.1 -jar /path/to/air-gap/air-gap-deduplication-fat-<version>.jar
   ```
3. **Enable and configure the Jolokia module:**
   ```sh
   sudo metricbeat modules enable jolokia
   sudo vi /etc/metricbeat/modules.d/jolokia.yml
   ```
   Example config:
   ```yaml
   - module: jolokia
     metricsets: [jmx]
     hosts: ["http://localhost:8778/jolokia"]
     namespace: "dedup"
     jmx.mappings:
       - mbean: 'java.lang:type=Memory'
         attributes:
           - attr: HeapMemoryUsage
             field: memory.heap_usage
      - mbean: 'nu.sitia.airgap:type=GapDetectors'
         attributes:
         - attr: 0_gaps
            field: partition_0_gaps
         - attr: 1_gaps
            field: partition_1_gaps
      - mbean: 'nu.sitia.airgap:type=Props'
         attributes:
         - attr: WINDOW_SIZE
            field: window_size
         - attr: MAX_WINDOWS
            field: max_windows             
   ```

If one common .service-file is used to start several instances of dedup with differnt .env files, make the following changes:
* For each .env-file, add `JAVA_OPTS=-javaagent:/opt/airgap/dedup/jolokia-agent.jar=port=8778,host=0.0.0.0`. Set the port and ip so they don't collide.
* Change the .service-file ExecStart to (set the version of the deduplication jar to the one you are using):
```sh
ExecStart=/usr/bin/java $JAVA_OPTS -jar /opt/airgap/dedup/air-gap-deduplication-fat-0.1.3-SNAPSHOT.jar
```

Now, each instance of the deduplicator will have it's own listening port.

The deduplicator will report how many windows each partition has when queried, as well as an estimate of how many bytes RAM the windows are currently using. The estimation is just an estimate but can be useful to spot trends in memory consumption. The application as a whole can be monitored for RAM usage by the system metrics for that process.

### Go Application Metrics
- Upstream and downstream are Go apps that may expose Prometheus metrics. The metrics that would be interesting are current load and number of processed events. That is a small win for more complexity. The most important metrics to observe are memory and processor consumption and those are handled by the system metrics module.

For now, no metrics will be exported from the Go applications to Prometheus.

## 3. Configure Metricbeat Output
Edit `/etc/metricbeat/metricbeat.yml` to send data to Elasticsearch or Logstash.

## 4. Start and Enable Metricbeat
```sh
sudo systemctl enable --now metricbeat
```

## 5. View Metrics
- Use Kibana or your preferred dashboard to visualize metrics.

---

### Using JMX Methods from JmxSupport.java in PartitionDedupApp

The PartitionDedupApp exposes rich runtime information and operations via JMX, thanks to the `JmxSupport` class. You can access these via JConsole, Jolokia, or any JMX client.

#### What is Exposed?
- **GapDetectors MBean** (`nu.sitia.airgap:type=GapDetectors`):
  - For each partition, exposes:
    - `<partition>`: Info about the GapDetector for that partition (window stats, offsets, etc.)
    - `<partition>_gaps`: Current gaps for that partition
    - Operations: `getAllGaps_<partition>()` and `purge_<partition>()` to fetch or purge gaps for a specific partition
- **Props MBean** (`nu.sitia.airgap:type=Props`):
  - All Kafka Streams properties
  - Topics, assigned partitions, window size, max windows, and other runtime config

#### How to Use
- **With JConsole:**
  1. Start your app with JMX enabled (or with Jolokia for remote HTTP access).
  2. Open JConsole and connect to the running JVM.
  3. Browse to `nu.sitia.airgap -> GapDetectors` or `Props` to view attributes and invoke operations.
- **With Jolokia (for Metricbeat):**
  - The Jolokia agent exposes these MBeans over HTTP. Metricbeat can be configured to scrape specific attributes or call operations.
  - Example: To fetch all gaps for partition 0, configure Metricbeat's `jolokia.yml` to query the `getAllGaps_0` operation on the `nu.sitia.airgap:type=GapDetectors` MBean.

#### Example Jolokia Query (HTTP API)
To call an operation (e.g., get all gaps for partition 0):
```sh
curl -X POST http://localhost:8778/jolokia/ \
  -H 'Content-Type: application/json' \
  -d '{"type":"exec","mbean":"nu.sitia.airgap:type=GapDetectors","operation":"getAllGaps_transfer_0"}'
```
To read an attribute (some examples):
```sh
curl http://127.0.0.1:8778/jolokia/read/java.lang:type=Memory
curl http://localhost:8778/jolokia/list/nu.sitia.airgap
curl http://localhost:8778/jolokia/read/nu.sitia.airgap:type=GapDetectors/transfer_0
curl http://localhost:8778/jolokia/read/nu.sitia.airgap:type=GapDetectors/transfer_0_mem
curl http://localhost:8778/jolokia/read/nu.sitia.airgap:type=GapDetectors/transfer_0_gaps
curl http://localhost:8778/jolokia/read/nu.sitia.airgap:type=Props
```

#### Example Metricbeat Mapping
Add to your `jolokia.yml`:
```yaml
- module: jolokia
  metricsets: [jmx]
  hosts: ["http://localhost:8778/jolokia"]
  namespace: "airgap"
  jmx.mappings:
    - mbean: 'nu.sitia.airgap:type=GapDetectors'
      attributes:
        - attr: 0_gaps
          field: partition_0_gaps
        - attr: 1_gaps
          field: partition_1_gaps
    - mbean: 'nu.sitia.airgap:type=Props'
      attributes:
        - attr: WINDOW_SIZE
          field: window_size
        - attr: MAX_WINDOWS
          field: max_windows
```

**Tip:** You can use JConsole or Jolokia to purge gaps or inspect all runtime config and state, per partition, without restarting the app.

---

**References:**
- [Metricbeat Jolokia module](https://www.elastic.co/guide/en/beats/metricbeat/current/metricbeat-module-jolokia.html)
- [Metricbeat Prometheus module](https://www.elastic.co/guide/en/beats/metricbeat/current/metricbeat-module-prometheus.html)
- [Jolokia documentation](https://jolokia.org/)
- [Prometheus client for Go](https://prometheus.io/docs/guides/go-application/)

