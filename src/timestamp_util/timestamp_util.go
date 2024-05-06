package timestamp_util

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strings"
)

// Read the configuration file and return the configuration
// In the configuration we have a from field. This is the timestamp
// we want to start reading from. The format is a standard time format
// in Go. Example: 2020-01-01T15:04:05-07:00
// When we use gap-detector to find gaps in the data, we want to start
// from the last gap we found. This is the timestamp we want to save
// in the configuration file.

// Create an updated array of lines from the configuration file, with the from
// configuration updated with the supplied newTime string
func UpdateTimeParameter(fileName string, newTime string) ([]string, error) {
    file, err := os.Open(fileName)
    if err != nil {
        return nil, err
    }
    defer file.Close()

    result := []string{}
    scanner := bufio.NewScanner(file)
    for scanner.Scan() {
        line := scanner.Text()
        parts := strings.SplitN(line, "=", 2)
        if len(parts) != 2 {
            result = append(result, line)    
            continue
        }
        key := strings.TrimSpace(parts[0])

        if (key == "from") {
            result = append(result, fmt.Sprintf("from=%s", newTime))
        } else {
            result = append(result, line)
        }        
    }
    
    if err := scanner.Err(); err != nil {
        return nil, err
    }
    return result, nil
}


// Write the result array to fileName
func WriteResultToFile(fileName string, result []string) error {
    file, err := os.OpenFile(fileName, os.O_WRONLY|os.O_TRUNC, 0644)
    if err != nil {
        return err
    }
    defer file.Close()

    writer := bufio.NewWriter(file)
    for _, line := range result {
        _, err := writer.WriteString(line + "\n")
        if err != nil {
            return err
        }
    }
    return writer.Flush()
}

func SaveTimestampInConfig(configFileName string, ts string) error {
    verbose := false
    // Update the timestamp in the config
    if (verbose) {
        log.Printf("Update the timestamp to %s", ts)
    }
    newConfig, err := UpdateTimeParameter(configFileName, ts)
    if (err == nil) {
        err2 := WriteResultToFile(configFileName, newConfig)
        if (err2 == nil) {
            if (verbose) {
                log.Printf("Successfully updated %s with the value from=%s", configFileName, ts)
            }
        } else {
            log.Printf("Error updating the file %s, err: %s", configFileName, err2)
            return err2
        }
    } else {
        log.Printf("Error updating the config with the new from value '%s', err: %s", ts, err)
        return err
    }
    return nil
}
