# Kafka Mutual Authentication and TLS
Some environments require that the communication with Kafka have to use TLS. They can also require mandatory authentication and authorizaion. This is a small guide to set up mutual authentication and autorization between air-gap and Kafka with self signed certificates.

This guid covers broker certificates, not controller certificates. If your Kafka require certificates for controllers, there are lots of guides for that on the Internet.

N.B., in this manual I don't aim to set up a working production environment, just a test setup to show how the certificates can be used to achieve TLS and mTLS. Please, do NOT use this manual as a blueprint for a production setting, since several properties are not suitable for production. For example: the key length is 2048 for the test certificates and should be at least 4096 in production. The validity length is set to almost 10 years - in a production environment you would like at most 1 year but maybe even weeks or days (if you have auto-renew on your certificates).

## TLS
To encrypt the connection between air-gap and Kafka we will need certificates on at least the Kafka machines. The clients (air-gap) need to be able to validate the certificates so they will need a truststore with the issuer of the Kafka certificates. If you don't have a certificate department or vendor, then we can create all certificates ourself. First, we need a root ca: Certificate Authority.

### Certificat Authority
We need to create a Certificat Authority that will sign all our certificates. In production environments, this is usually made on a non-networked computer that is in a locked cabinet when not used.

### Generate CA private key
```bash
openssl genrsa -out kafka-ca.key 4096
```

### Generate self-signed CA certificate
```bash
openssl req -x509 -new -nodes -key kafka-ca.key -sha256 -days 3650 \
  -out kafka-ca.crt -subj "/CN=MyKafkaCA"
```

### Create Kafka Broker Certificates
Each Kafka broker needs its own cert signed by the CA. Since some organizations use CN (mostly older) and others SAN, Subject Alternative Name, for authentication and authorization, we will create our certificates with both and also test that we can extract the user id from both (see below).

For both SAN and CN, we first create a file for each broker, kafka-upstream and kafka-downstream. Note that the CN must be the same as the hostnames in DNS or the hosts file.

#### kafka-upstream.cnf
```ini
[ req ]
default_bits       = 2048
prompt             = no
default_md         = sha256
req_extensions     = req_ext
distinguished_name = dn

[ dn ]
CN = kafka-upstream.mydomain.com

[ req_ext ]
subjectAltName = @alt_names

[ alt_names ]
DNS.1 = kafka-upstream.mydomain.com
DNS.2 = kafka-upstream.mydomain.com
```

#### kafka-downstream.cnf
```ini
[ req ]
default_bits       = 2048
prompt             = no
default_md         = sha256
req_extensions     = req_ext
distinguished_name = dn

[ dn ]
CN = kafka-downstream.mydomain.com

[ req_ext ]
subjectAltName = @alt_names

[ alt_names ]
DNS.1 = kafka-downstream.mydomain.com
DNS.2 = kafka-downstream.mydomain.com
```


```bash
# Create the keys
openssl genrsa -out kafka-upstream.key 2048
openssl genrsa -out kafka-downstream.key 2048

# Create Signing Requests
openssl req -new -key kafka-upstream.key \
  -out kafka-upstream.csr \
  -config kafka-upstream.cnf

openssl req -new -key kafka-downstream.key \
  -out kafka-downstream.csr \
  -config kafka-downstream.cnf

# Sign with CA for 10 years (not for production)
# Make sure SAN is included by specifying extensions during signing
openssl x509 -req -in kafka-upstream.csr -CA kafka-ca.crt -CAkey kafka-ca.key \
  -CAcreateserial -out kafka-upstream.crt -days 3650 -sha256 \
  -extfile kafka-upstream.cnf -extensions req_ext

openssl x509 -req -in kafka-downstream.csr -CA kafka-ca.crt -CAkey kafka-ca.key \
  -CAcreateserial -out kafka-downstream.crt -days 3650 -sha256 \
  -extfile kafka-downstream.cnf -extensions req_ext

# Inspect the certificates
openssl x509 -in kafka-upstream.crt -text -noout
openssl x509 -in kafka-downstream.crt -text -noout
```

#### Create the client certificates
Each client also needs it's own certificates. Since we would like both SAN and CN available for authentication, we need the cnf files here too:

##### airgap-upstream.cnf
```ini
[ req ]
default_bits       = 2048
prompt             = no
default_md         = sha256
req_extensions     = req_ext
distinguished_name = dn

[ dn ]
CN = airgap-upstream-client

[ req_ext ]
subjectAltName = @alt_names

[ alt_names ]
DNS.1 = airgap-upstream-client
DNS.2 = airgap-upstream-client
```


#### airgap-downstream.cnf
```ini
[ req ]
default_bits       = 2048
prompt             = no
default_md         = sha256
req_extensions     = req_ext
distinguished_name = dn

[ dn ]
CN = airgap-downstream-client

[ req_ext ]
subjectAltName = @alt_names

[ alt_names ]
DNS.1 = airgap-downstream-client
DNS.2 = airgap-downstream-client
```

```bash
# Client key
openssl genrsa -out airgap-upstream.key 2048
openssl genrsa -out airgap-downstream.key 2048

# CSR
openssl req -new -key airgap-upstream.key \
  -out airgap-upstream.csr \
  -config airgap-upstream.cnf

openssl req -new -key airgap-downstream.key \
  -out airgap-downstream.csr \
  -config airgap-downstream.cnf

# Sign
openssl x509 -req -in airgap-upstream.csr -CA kafka-ca.crt -CAkey kafka-ca.key \
  -CAcreateserial -out airgap-upstream.crt -days 3650 -sha256 \
  -extfile airgap-upstream.cnf -extensions req_ext

openssl x509 -req -in airgap-downstream.csr -CA kafka-ca.crt -CAkey kafka-ca.key \
  -CAcreateserial -out airgap-downstream.crt -days 3650 -sha256 \
  -extfile airgap-downstream.cnf -extensions req_ext

# Inspect the certificates
openssl x509 -in airgap-upstream.crt -text -noout
openssl x509 -in airgap-downstream.crt -text -noout

```
All upstream and downstream certificates should contain `X509v3 Subject Alternative Name: ...` and `Subject: CN=...`

In order to be able to load the certificates into Kafka, we need to create a keystore and we also need a truststore for Kafka to trust client certificates. When generating the p12-files and keystores, you will be prompted for a password. Save the passwords for the files and keystores. When generating the keystores you will also need to supply the password for the p12 file that it imports.

#### Java Keystore and Truststore
```bash
# Kafka keystore (contains Kafka’s private key + cert)
openssl pkcs12 -export -in kafka-upstream.crt -inkey kafka-upstream.key \
  -chain -CAfile kafka-ca.crt -name kafka-upstream -out kafka-upstream.p12

keytool -importkeystore -destkeystore kafka-upstream.keystore.jks \
  -srckeystore kafka-upstream.p12 -srcstoretype PKCS12 \
  -alias kafka-upstream

# Broker truststore (contains CA to trust clients)
keytool -import -trustcacerts -alias CARoot -file kafka-ca.crt \
  -keystore kafka-upstream.truststore.jks

# Inspect the keystore:
keytool -list -v -keystore kafka-upstream.keystore.jks

# Inspect the truststore:
keytool -list -v -keystore kafka-upstream.truststore.jks
```
and for downstream:
```bash
# Kafka keystore (contains Kafka’s private key + cert)
openssl pkcs12 -export -in kafka-downstream.crt -inkey kafka-downstream.key \
  -chain -CAfile kafka-ca.crt -name kafka-downstream -out kafka-downstream.p12

keytool -importkeystore -destkeystore kafka-downstream.keystore.jks \
  -srckeystore kafka-downstream.p12 -srcstoretype PKCS12 \
  -alias kafka-downstream

# Broker truststore (contains CA to trust clients)
keytool -import -trustcacerts -alias CARoot -file kafka-ca.crt \
  -keystore kafka-downstream.truststore.jks

# Inspect the keystore:
keytool -list -v -keystore kafka-downstream.keystore.jks

# Inspect the truststore:
keytool -list -v -keystore kafka-downstream.truststore.jks
```

### Generate certificates for the air-gap application
air-gap is written in Golang and uses pem certificates. The crt files are actually in pem format. If you open the crt file you can see that it starts with `-----BEGIN CERTIFICATE-----`. 

air-gap will use the .key and .crt files for authenticating to Kafka.

## Configure Kafka for TLS
Copy the files:
- kafka-upstream.keystore.jks
- kafka-upstream.truststore.jks

to `/opt/kafka/config/ssl/` on the upstream Kafka machine(s)

Edit `server.properties`for each Kafka broker and add (change the domain name to your domain name):
```properties
listeners=SSL://0.0.0.0:9093
advertised.listeners=SSL://kafka-upstream.mydomain.com:9093
ssl.keystore.location=/opt/kafka/config/ssl/kafka-upstream.keystore.jks
ssl.keystore.password=changeit
ssl.key.password=changeit
ssl.truststore.location=/opt/kafka/config/ssl/kafka-upstream.truststore.jks
ssl.truststore.password=changeit

# Require client authentication (mTLS)
#ssl.client.auth=required
```

In the server.properties file you need to add the SSL protocol as a broker protocol. Please don't try to set the same port for controller and broker. That will not work. The default configuration for Kafka is 9092 for PLAINTEXT and 9093 for CONTROLLER. Usually, 9093 will be used for SSL but if you do, you need to change the CONTROLLER prot to, e.g., 9080 and maybe 9093 for CONTROLLER SSL, if used. If you try to use 9093 for both CONTROLLER and BROKER SSL you might get:
```
ERROR Error processing message, terminating consumer process:  (org.apache.kafka.tools.consumer.ConsoleConsumer)
org.apache.kafka.common.errors.UnsupportedVersionException: The node does not support METADATA
```

If you want to run the Kafka console applications, you will need a producer.ssl.config and a consumer.ssl.config. For Kafka, you will need your airgap-client certificates in pkcs#12 format with a keystore and a truststore. This is highly recommended to be able to create and view topics from the command line.

```bash
# Create pkcs#12 version of airgap-upstream.crt and key
openssl pkcs12 -export -in airgap-upstream.crt -inkey airgap-upstream.key \
  -chain -CAfile kafka-ca.crt -name airgap-upstream -out airgap-upstream.p12

# Create a keystore that contains the airgap-upstream key and certificate
keytool -importkeystore -destkeystore airgap-upstream.keystore.jks \
  -srckeystore airgap-upstream.p12 -srcstoretype PKCS12 \
  -alias airgap-upstream

# Create a truststore for airgap-upstream that contains the kafka-upstream certificate issuer (our root CA)
keytool -import -trustcacerts -alias CARoot -file kafka-ca.crt \
  -keystore airgap-upstream.truststore.jks
```
Copy the files:
- airgap-upstream.keystore.jks
- airgap-upstream.truststore.jks

to `/opt/kafka/config/ssl/` on the upstream Kafka machine(s) or where you want to use the Kafka command line utilities.

Create a new file: `/opt/kafka/config/producer.ssl.properties`
```properties
security.protocol=SSL
ssl.keystore.location=/opt/kafka/config/ssl/airgap-upstream.keystore.jks
ssl.keystore.password=changeit
ssl.key.password=changeit
ssl.truststore.location=/opt/kafka/config/ssl/airgap-upstream.truststore.jks
ssl.truststore.password=changeit
```

Now you should be able to restart Kafka and run
```bash
bin/kafka-console-consumer.sh --topic downstream --bootstrap-server kafka-upstream.mydomain.com:9093 --consumer.config ./config/producer.ssl.properties --from-beginning
```
The command should give the same output as if you ran it on the plaintext port:
```bash
bin/kafka-console-consumer.sh --topic downstream --bootstrap-server kafka-upstream.mydomain.com:9092  --from-beginning
```

Now, we up the difficulty a bit. We add authorization so clients not only need to have a certificate from a valid issues but also need to be present in an allow-list.

## Authorization
When we created the airgap-* certificates, we added a name to them: `airgap-upstream-client` and `airgap-downstream-client`. We will now use those as identifiers in Kafka for authorization.

To be continued...