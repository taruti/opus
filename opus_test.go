package opus

import "math"
import "os"
import "testing"

func TestOpusEncode(t *testing.T) {
	enc, err := NewEncoder(DefaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()

	fs := make([]float32, 1920*2)

	// A = 440Hz
	// sample rate 48khz -> ~110
	f := 2 * math.Pi / 110.0
	for i := 0; i < len(fs); i += 2 {
		s := float64((i / 2) % 110)
		fs[i] = float32(0.5 * math.Sin(s*f))
	}

	for i := 0; i < 10; i++ {
		bs, err := enc.EncodeFloat(fs)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("Opus encode produced %d\n", len(bs))
	}

}

func TestOpusOggEncode(t *testing.T) {
	out, e := os.Create("/tmp/test.ogg")
	if e != nil {
		t.Fatal(e)
	}
	defer out.Close()

	enc, err := NewEncoder(DefaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()

	_, err = out.Write(enc.StreamHeader())
	if err != nil {
		t.Fatal(err)
	}

	fs := make([]float32, 1920*2)

	// A = 440Hz
	// sample rate 48khz -> ~110
	f := 2 * math.Pi / 110.0
	for i := 0; i < len(fs); i += 2 {
		s := float64((i / 2) % 110)
		fs[i] = float32(0.5 * math.Sin(s*f))
	}

	for i := 0; i < 10; i++ {
		bs, err := enc.EncodeFloat(fs)
		if err != nil {
			t.Fatal(err)
		}
		_, err = out.Write(bs)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("Ogg encode produced %d\n", len(bs))
	}
}
