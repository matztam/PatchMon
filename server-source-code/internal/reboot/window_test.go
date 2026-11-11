package reboot

import (
	"testing"
	"time"
)

func TestInWindow(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata")
	}
	all := []int32{0, 1, 2, 3, 4, 5, 6}
	sunday := []int32{0}
	// 2026-11-08 is a Sunday.
	at := func(day, hour, minute int) time.Time {
		return time.Date(2026, 11, day, hour, minute, 0, 0, berlin)
	}
	tests := []struct {
		name       string
		now        time.Time
		days       []int32
		start, end string
		want       bool
	}{
		{"inside", at(8, 3, 0), all, "02:00", "05:00", true},
		{"start is inclusive", at(8, 2, 0), all, "02:00", "05:00", true},
		{"end is exclusive", at(8, 5, 0), all, "02:00", "05:00", false},
		{"before", at(8, 1, 59), all, "02:00", "05:00", false},
		{"wrong day", at(9, 3, 0), sunday, "02:00", "05:00", false},
		{"right day", at(8, 3, 0), sunday, "02:00", "05:00", true},
		{"over midnight, evening of the day", at(8, 23, 30), sunday, "23:00", "02:00", true},
		{"over midnight, morning after belongs to the day before", at(9, 1, 0), sunday, "23:00", "02:00", true},
		{"over midnight, morning of the day itself is not in it", at(8, 1, 0), sunday, "23:00", "02:00", false},
		{"over midnight, outside", at(8, 12, 0), sunday, "23:00", "02:00", false},
		{"whole day", at(8, 12, 0), sunday, "00:00", "00:00", true},
		{"whole day, other day", at(9, 12, 0), sunday, "00:00", "00:00", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := InWindow(tt.now.UTC(), berlin, tt.days, tt.start, tt.end)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("InWindow(%s) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}

func TestInWindowUsesLocation(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no tzdata")
	}
	// 01:30 UTC is 02:30 in Berlin (CET).
	now := time.Date(2026, 11, 8, 1, 30, 0, 0, time.UTC)
	if in, _ := InWindow(now, berlin, []int32{0}, "02:00", "05:00"); !in {
		t.Fatal("window should be open in Berlin time")
	}
	if in, _ := InWindow(now, time.UTC, []int32{0}, "02:00", "05:00"); in {
		t.Fatal("window should be closed in UTC")
	}
}

func TestParseClock(t *testing.T) {
	for _, bad := range []string{"", "2", "24:00", "12:60", "1:5", "ab:cd", "12:5"} {
		if _, err := ParseClock(bad); err == nil {
			t.Errorf("ParseClock(%q) succeeded", bad)
		}
	}
	if m, err := ParseClock("02:30"); err != nil || m != 150 {
		t.Fatalf("ParseClock(02:30) = %d, %v", m, err)
	}
}

func TestValidateDays(t *testing.T) {
	if ValidateDays(nil) == nil || ValidateDays([]int32{7}) == nil || ValidateDays([]int32{-1}) == nil {
		t.Fatal("invalid days accepted")
	}
	if err := ValidateDays([]int32{0, 6}); err != nil {
		t.Fatal(err)
	}
}

func TestCleanMessage(t *testing.T) {
	if CleanMessage("  \n ") != nil {
		t.Fatal("blank message should be nil")
	}
	if got := *CleanMessage(" a \n b "); got != "a b" {
		t.Fatalf("got %q", got)
	}
	long := make([]rune, 300)
	for i := range long {
		long[i] = 'x'
	}
	if got := *CleanMessage(string(long)); len([]rune(got)) != MaxMessageLength {
		t.Fatalf("length %d", len([]rune(got)))
	}
}
