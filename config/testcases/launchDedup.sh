#!/bin/bash

# Script to launch/stop multiple instances of the deduplication service
# Usage:
#   ./launchDedup.sh start <number_of_instances>
#   ./launchDedup.sh stop

ACTION=$1
PIDFILE="$(dirname "$0")/dedup-pids.txt"

DIR="$(dirname "$0")"
ENV_FILE="$DIR/dedup-16.env"
JAR_PATH="/opt/airgap/dedup/air-gap-deduplication-fat-0.1.5-SNAPSHOT.jar"

if [ "$ACTION" = "start" ]; then
  if [ "$#" -ne 2 ]; then
    echo "Usage: $0 start <number_of_instances>"
    exit 1
  fi
  NUM_INSTANCES=$2
  > "$PIDFILE" # Truncate PID file
  for ((i=1; i<=NUM_INSTANCES; i++)); do
    # Prepare environment variables for this instance
    ENV_VARS=""
    STATE_DIR_CONFIG_VALUE=""
    while IFS= read -r line; do
      [[ "$line" =~ ^#.*$ || -z "$line" ]] && continue
      kv=$(echo $line | sed "s/{id}/$i/g")
      ENV_VARS="$ENV_VARS $kv"
      # Capture STATE_DIR_CONFIG for cleanup
      if [[ $kv == STATE_DIR_CONFIG=* ]]; then
        STATE_DIR_CONFIG_VALUE="${kv#STATE_DIR_CONFIG=}"
      fi
    done < "$ENV_FILE"
    echo "Resetting state directory for instance $i: $STATE_DIR_CONFIG_VALUE"
    rm -rf "$STATE_DIR_CONFIG_VALUE"
    mkdir -p "$STATE_DIR_CONFIG_VALUE"

    echo "Launching instance $i with $ENV_VARS"
    env $ENV_VARS java -Dlog4j.configurationFile=$DIR/log4j2.xml -jar "$JAR_PATH" &
    PID=$!
    echo $PID >> "$PIDFILE"
  done
  echo "All instances launched. PIDs recorded in $PIDFILE."
elif [ "$ACTION" = "stop" ]; then
  if [ ! -f "$PIDFILE" ]; then
    echo "No PID file found. Are any instances running?"
    exit 1
  fi
  while read -r PID; do
    if kill -0 "$PID" 2>/dev/null; then
      echo "Stopping instance with PID $PID"
      kill "$PID"
    fi
  done < "$PIDFILE"
  rm -f "$PIDFILE"
  echo "All instances stopped."
else
  echo "Usage: $0 start <number_of_instances> | stop"
  exit 1
fi
