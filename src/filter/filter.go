package main

// This program is a simple filter that can be used to filter out packets
// based on a configuration. The configuration is a comma separated list of
// numbers. The numbers are divided into three groups of id numbers you would
// like to send. Example: You want to send every other packet with two different
// senders (static load balancing):
// Sender 1 is configured with: 1,3,5
// Sender 2 is configured with: 2,4,6
// Now, the first sender will drop packets with id 2, 4, 6 and the second sender
// will drop packets with id 1, 3, 5.
// Also, when packets with higher numbers are found, we use the modulo operator
// so packets from sender 1 will be 1, 3, 5, 7, 9, ... and so forth.

// We can also use this to configure redundancy. Example, we have three senders
// and we want to deliver every packet twice. We can configure the first
// sender with 1,2,4,5,7,8 the second with 2,3,5,6,8,9 and the third
// with 3,4,6,7,9,10. Now, every packet will be delivered twice over three paths.


import (
	"log"
	"strconv"
	"strings"
)

func isValidNumber(number int64, groups [][]int64, k int64) bool {
    // Get the remainder of the division
    pos := ((number - 1) % int64(k)) + 1
    for i := 0; i < len(groups[0]); i++ {
        if groups[0][i] == pos {
            return true
        }
    }
    return false
}

func inGroup(nr int64, groups [][]int64) bool {
    for i := 0; i < len(groups); i++ {
        for j:= 0; j < len (groups[i]); j++ {
            if (nr == groups[i][j]) {
                return true;
            }
        }
    }
    return false;
}

func doSelfTest(groups [][]int64, k int64) bool {
    largestNumber := int(groups[2][len(groups[2])-1]) // Convert largestNumber to int
    // Perform self test logic here
    for i := 0; i < largestNumber; i++ {
        valid := isValidNumber(int64(i), groups, k) // Convert i to int64
        isInGroups := inGroup(int64(i), groups) // Convert i to int64
        log.Printf("%d: isValid returned %v inGroup returned %v",
            i, valid, isInGroups)
        if valid != isInGroups {
            log.Printf("%d: isValid returned %v inGroup returned %v",
                i, valid, isInGroups)
            return false
        }
    }
    return true
}


// Test the filter from the command line. Here, we test the filter with a
// configuration of 2,3,22,23,42,43, that is, we should send the packets
// with id: 2, 3, 22, 23, 42 and 43. Other instances might send the packets
// 1, 4, 21, 24, 41 and 44 and so on. It would be sufficient to just add one
// iteratio (2,3,22,23) but the last group is added to verify that the user
// has entered the correct configuration.
func main() {
    config := "2,3,22,23,42,43"
    parts := strings.Split(config, ",")

    itemsPerGroup := len(parts) / 3
    if len(parts)%3 != 0 {
        log.Fatal("Number of items should be divisible by 3")
    }
    // groups is an array of size 3 with int arrays as items
    groups := make([][]int64, 3)
    for j := 0; j < 3; j++ {
        intArray := make([]int64, itemsPerGroup)
        for i := 0; i < itemsPerGroup; i++ {
            intValue, _ := strconv.Atoi(parts[j*itemsPerGroup + i])
            intArray[i] = int64(intValue)
        }
        groups[j] = intArray
    }

    k := groups[1][0] - groups[0][0]
    if groups[2][0] != groups[1][0]+k {
        log.Fatalf("The third group starts with an unexpected number. Got %d but expected %d",
            groups[2][0], groups[1][0]+k)
    }
    if !doSelfTest(groups, k) {
        log.Fatalf("The self test of the expression %s failed", config)
    } else {
        log.Printf("Self test of the expression ok")
    }
    // from := 23413421342141234
    // for i := from; i < 20+ from; i++ {
    //     log.Printf("%d -> %v", i, isValidNumber(int64(i), groups, k))
    // }
}
