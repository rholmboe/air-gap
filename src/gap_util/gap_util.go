package gap_util

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"sitia.nu/airgap/src/timestamp_util"
)

var timeStamp time.Time
var verbose = true
var reportEventsAsMissingAfterMs = 30000
var Logger = log.New(os.Stdout, "", log.LstdFlags)

func SetLogger(newLogger *log.Logger) {
	Logger = newLogger
}

type Gap struct {
    From            int64   // row number in a partition
    To              int64   // row number in a partition
    Timestamp       time.Time // time when we discovered the missing event
}

type Gaps struct {
    ExpectedNumber  int64 // Next number we anticipate for this topic-partition
    Gaps            []Gap
}
// Array of topic_partitionId -> allGaps
var allGaps = map[string] Gaps {}
var mu sync.RWMutex

// Timestamp for the last read event in input topic
func SetTimestamp(newTime time.Time) {
    timeStamp = newTime
}

// Return a copy of one Gaps instance, specified
// by the key
func GetAllGaps(key string) Gaps {
    holder := allGaps[key]
    mu.Lock() // We don't want partly updated state
    result := Gaps {
        ExpectedNumber: holder.ExpectedNumber,
        Gaps: []Gap{},
    }
    for i := range holder.Gaps {
        currentGap := holder.Gaps[i]
        copy := Gap {
            From:       currentGap.From,
            To:         currentGap.To,
            Timestamp:  currentGap.Timestamp,
        }
        result.Gaps = append(result.Gaps, copy)
    }
    mu.Unlock()
    return result
}

// Update the gaps to reflect the newly received number
// Return true iff the message has already been received (duplicate)
func CheckNextNumber(key string, number int64) bool {
    var verbose = false // override verbose. Set to false in production
    var returnValue bool = false
    if (verbose) {
        Logger.Printf("checkNextNumber %s %d", key, number)
    }
    currentGaps, ok := allGaps[key]
    if (ok == false) {
        if (verbose) {
            Logger.Printf("Adding new currentGaps with key %s", key)
        }
        // not found, initialize a new Gaps struct
        currentGaps = Gaps{
            ExpectedNumber: 0,
            Gaps:           []Gap{},
        }
    } else {
        if (verbose) {
            Logger.Printf("Using stored currentGaps with expectedNumber: %d", currentGaps.ExpectedNumber)
        }
    }
    // Check if the currentGaps->number is the next expected
    if (currentGaps.ExpectedNumber == number) {
        if (verbose) {
            Logger.Printf("received number %d was the expected", number)
        }
        // Yes, update the struct so we expect the next number
        currentGaps.ExpectedNumber++
    } else if (number > currentGaps.ExpectedNumber) {
        // We have a gap
        gap := Gap {
            From:       currentGaps.ExpectedNumber,
            To:         (number - 1),
            Timestamp:  time.Now(),
        }
        if (verbose) {
            Logger.Printf("Gap detected. Adding from %d to %d", gap.From, gap.To)
        }
        mu.Lock()
        currentGaps.Gaps = append(currentGaps.Gaps, gap)
        currentGaps.ExpectedNumber = number + 1
        mu.Unlock()
    } else {
        // the number is less than the expected number. This
        // Might be a duplicate, or it might be a previously 
        // missing event
        gapNumber, err := getGapForNumber(currentGaps.Gaps, number)
        if (err == nil) {
            gap := currentGaps.Gaps[gapNumber]
            // There is a gap for this number
            if (number == gap.From && number == gap.To) {
                if (verbose) {
                    Logger.Printf("Number is this gap, remove gap")
                }
                // Gap was missing just this number
                // remove the gap 
                mu.Lock()
                currentGaps.Gaps = append(currentGaps.Gaps[:gapNumber], currentGaps.Gaps[gapNumber+1:]...)
                mu.Unlock()
            } else if (number == gap.From && number != gap.To) {
                // just remove the from. There are other missing numbers too
                if (verbose) {
                    Logger.Printf("Number is the start of this gap, adding from")
                }
                gap.From += 1
                mu.Lock()
                currentGaps.Gaps[gapNumber] = gap
                mu.Unlock()
            } else if (number != gap.From && number == gap.To) {
                // missing just the last one
                if (verbose) {
                    Logger.Printf("Number is the end of this gap, subtracting to")
                }
                gap.To -= 1
                mu.Lock()
                currentGaps.Gaps[gapNumber] = gap
                mu.Unlock()
            } else {
                // The gap is more than 2 long and the received item is in the middle of the gap
                // Split the gap into two gaps with from .. number-1, number+1 .. to
                if (verbose) {
                    Logger.Printf("Creating new gap")
                }
                newGap := Gap {
                    From:       number+1,
                    To:         gap.To,
                    Timestamp:  gap.Timestamp,
                }
                gap.To = number-1
                // add the gap 
                mu.Lock()
                // The line above setting gap.To won't update the array item
                currentGaps.Gaps[gapNumber] = gap
                currentGaps.Gaps = append(currentGaps.Gaps, newGap)
                mu.Unlock()
            }
        } else {
            // The return value is not in any gap and less than the expected one
            // This is a duplicate
            returnValue = true
        }
    }
    mu.Lock()
    allGaps[key] = currentGaps
    mu.Unlock()
    return returnValue
}

func GetFirstGaps() (int64, [] string) {
    result := []string{}
    total := int64(0)
    // We don't want any other thread to update 
    // the information during this function
    fromTime := time.Now().Add(-time.Duration(reportEventsAsMissingAfterMs) * time.Millisecond)
    mu.Lock()
    for key := range allGaps {
        holder := allGaps[key]
        min := int64(-1)
        for j := range holder.Gaps {
            gap := holder.Gaps[j]
            if gap.From < min || min == -1 {
                if (gap.Timestamp.Before(fromTime)) {
                    // The gap was found reportEventsAsMissingAfterMs milliseconds ago
                    // Report this one:
                    min = gap.From
                }
            }
            total += gap.To - gap.From
        }
        if (min > -1) {
            result = append(result, key + "_" + fmt.Sprintf("%d",min))
        }
    }
    mu.Unlock()
    return total, result
}

func getGapForNumber(gaps []Gap, number int64) (int, error) {
    for i := range gaps {
        gap := gaps[i]
        if (number >= gap.From  && number <= gap.To) {
            // Found it
            return i, nil
        }
    }
    return -1, errors.New("Not found")
}

func Save(fileName string, configFileName string) error {
    bytes, err := json.Marshal(allGaps)
    if (err != nil) {
        return err
    }
    f, error := os.Create(fileName)
    if (error != nil) {
        return error
    }
    defer f.Close()
    f.Write(bytes)
    
    // Update the timestamp in the config
    ts := timeStamp.Format("2006-01-02T15:04:05Z07:00")
    return timestamp_util.SaveTimestampInConfig(configFileName, ts)
}

func Load(fileName string) error {
    ex, err := os.Executable()
    if (err != nil) {
        Logger.Fatal(err)
    }
    path := filepath.Dir(ex)
    Logger.Printf("gap_detector starting in directory: %s", path)
    Logger.Printf("Trying to load gap file: %s", fileName)
    _, err = os.Stat(fileName)
    if errors.Is(err, os.ErrNotExist) {
        // File doesn't exist (new installation)
        Logger.Printf("WARNING: No Gap file found at: %s %s", fileName, err)
        return nil
    }
    content, error := os.ReadFile(fileName)
    if (error != nil) {
        return error
    }
    err = json.Unmarshal(content, &allGaps)
    if (err != nil) {
        return error
    }
    return nil
}
