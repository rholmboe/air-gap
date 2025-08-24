package test

import (
	"testing"

	"sitia.nu/airgap/src/gap_util"
)

func TestAll(t *testing.T) {
    // Test case 1: valid id
    key := "topic_partition"
	number := int64(0);
	doTest(t, key, number, false)

	number = int64(1); 
	doTest(t, key, number, false)

	// Test with next number
	number = int64(2)
	doTest(t, key, number, false)

	// Test with a gap
	number = int64(4)
	doTest(t, key, number, false)

	// Check that we have one valid gap
	gaps := gap_util.GetAllGaps(key)
	if (len(gaps.Gaps) != 1) {
		t.Errorf("Wrong number of gaps. Expected %d but got %d", 1, len(gaps.Gaps))
	}

	// Check the From and To values in the gap
	if (gaps.Gaps[0].From != 3) {
		t.Errorf("Wrong From in the gap. Expected %d but got %d", 
			3, gaps.Gaps[0].From)
	}
	if (gaps.Gaps[0].To != 3) {
		t.Errorf("Wrong To in the gap. Expected %d but got %d", 
			3, gaps.Gaps[0].To)
	}

	// Now, send number 3 and check that we have no gaps
	doTest(t, key, int64(3), false)
	// GetAllGaps return a copy, so run that again
	gaps = gap_util.GetAllGaps(key)
	if (len(gaps.Gaps) != 0) {
		t.Errorf("Wrong number of gaps. Expected %d but got %d", 1, len(gaps.Gaps))
	}

//-------------------------------------
	// Test with a larger gap
	number = int64(10)
	doTest(t, key, number, false)

	// Check that we have one valid gap
	gaps = gap_util.GetAllGaps(key)
	if (len(gaps.Gaps) != 1) {
		t.Errorf("Wrong number of gaps. Expected %d but got %d", 1, len(gaps.Gaps))
	}

	// Check the From and To values in the gap
	if (gaps.Gaps[0].From != 5) {
		t.Errorf("Wrong From in the gap. Expected %d but got %d", 
			5, gaps.Gaps[0].From)
	}
	if (gaps.Gaps[0].To != 9) {
		t.Errorf("Wrong To in the gap. Expected %d but got %d", 
			9, gaps.Gaps[0].To)
	}

	// Now, send number 7 and check that we have two gaps
	doTest(t, key, int64(7), false)
	// GetAllGaps return a copy, so run that again
	gaps = gap_util.GetAllGaps(key)
	if (len(gaps.Gaps) != 2) {
		t.Errorf("Wrong number of gaps. Expected %d but got %d", 2, len(gaps.Gaps))
	}
	// Check the From and To values in the gap
	if (gaps.Gaps[0].From != 5) {
		t.Errorf("Wrong From in the gap. Expected %d but got %d", 
			5, gaps.Gaps[0].From)
	}
	if (gaps.Gaps[0].To != 6) {
		t.Errorf("Wrong To in the gap. Expected %d but got %d", 
			6, gaps.Gaps[0].To)
	}

	// Check the From and To values in the gap
	if (gaps.Gaps[1].From != 8) {
		t.Errorf("Wrong From in the gap. Expected %d but got %d", 
			8, gaps.Gaps[0].From)
	}
	if (gaps.Gaps[1].To != 9) {
		t.Errorf("Wrong To in the gap. Expected %d but got %d", 
			9, gaps.Gaps[0].To)
	}
	



	// And test with a duplicate
	doTest(t, key, number, true)
}

func doTest (t *testing.T, key string, number int64, expected bool) {
	check := gap_util.CheckNextNumber(key, number)
	if check != expected {
		t.Errorf("Expected CheckNextNumber returning %t, expected %t", check, expected)
	}
}