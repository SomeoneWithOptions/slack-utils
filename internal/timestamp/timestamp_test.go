package timestamp

import (
	"testing"
	"time"
)

func TestParseSlack(t *testing.T) {
	got, err := ParseSlack("1714557600.123456")
	want := time.Unix(1714557600, 123456000).UTC()
	if err != nil || !got.Equal(want) {
		t.Fatalf("ParseSlack() = %v, %v; want %v", got, err, want)
	}
	if _, err := ParseSlack("not-a-timestamp"); err == nil {
		t.Fatal("ParseSlack(invalid) returned no error")
	}
}

func TestParseBound(t *testing.T) {
	t.Run("RFC3339", func(t *testing.T) {
		got, err := ParseBound("2024-05-01T09:00:00Z", false)
		assertSlackTime(t, got, err, time.Date(2024, 5, 1, 9, 0, 0, 0, time.UTC))
	})

	t.Run("to date includes the whole day", func(t *testing.T) {
		got, err := ParseBound("2024-05-01", true)
		assertSlackTime(t, got, err, time.Date(2024, 5, 1, 23, 59, 59, 999999999, time.UTC))
	})

	t.Run("relative since", func(t *testing.T) {
		before := time.Now().UTC().Add(-2 * time.Hour)
		got, err := ParseBound("2h", false)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseSlack(got)
		if err != nil {
			t.Fatal(err)
		}
		if parsed.Before(before.Add(-time.Second)) || parsed.After(time.Now().UTC().Add(-2*time.Hour+time.Second)) {
			t.Fatalf("relative bound = %v, want approximately two hours ago", parsed)
		}
	})

	t.Run("relative to is rejected", func(t *testing.T) {
		if _, err := ParseBound("2h", true); err == nil {
			t.Fatal("ParseBound(relative to) returned no error")
		}
	})
}

func TestFormatMessagePreservesInvalidTimestamp(t *testing.T) {
	if got := FormatMessage("bad-ts"); got != "bad-ts" {
		t.Fatalf("FormatMessage(invalid) = %q, want original value", got)
	}
}

func assertSlackTime(t *testing.T, value string, err error, want time.Time) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseSlack(value)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Equal(want) {
		t.Fatalf("parsed bound = %v, want %v", got, want)
	}
}
