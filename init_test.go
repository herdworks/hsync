package hsync

import "testing"

func TestParseSize(t *testing.T) {
	t.Parallel()
	got, err := ParseSize("5MB")
	if err != nil {
		t.Fatal(err)
	}
	if want := int64(5 * 1024 * 1024); got != want {
		t.Fatalf("ParseSize() = %d, want %d", got, want)
	}
	if _, err := ParseSize("five"); err == nil {
		t.Fatal("expected invalid size error")
	}
}
