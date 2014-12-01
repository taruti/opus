// Encode raw pcm to opus (ogg) audio with cgo libopus binding.
package opus

//#include <opus/opus.h>
// #cgo LDFLAGS: -lopus
import "C"
import "errors"
import "unsafe"
import "encoding/binary"
import "hash/crc32"
import "time"

//import "log"

type Encoder struct {
	ctx *C.struct_OpusEncoder
	buf []byte
	EncoderConfig
	psn uint32
}

// 2880 * 2

const (
	APPLICATION_VOIP                = 2048
	APPLICATION_AUDIO               = 2049
	APPLICATION_RESTRICTED_LOWDELAY = 2051
	FRAMING_NONE                    = 0
	FRAMING_OGG                     = 1
)

type EncoderConfig struct {
	SamplingRate int
	Channels     int
	Application  int
	Framing      int
}

var DefaultConfig = EncoderConfig{48000, 2, APPLICATION_AUDIO, FRAMING_OGG}

var oggCrc32 = crc32.MakeTable(0x04c11db7)

func NewEncoder(c EncoderConfig) (*Encoder, error) {
	var ec C.int
	var op Encoder
	op.ctx = C.opus_encoder_create(C.opus_int32(c.SamplingRate), C.int(c.Channels), C.int(c.Application), &ec)
	if op.ctx == nil {
		return nil, errors.New("Creating opus encoder failed")
	}
	op.EncoderConfig = c
	op.buf = make([]byte, 256*1024)
	op.psn = 2
	return &op, nil
}

func (e *Encoder) Close() {
	if e == nil || e.ctx == nil {
		return
	}
	C.opus_encoder_destroy(e.ctx)
	e.ctx = nil
}

var srates = [...]int{2880, 1920, 960, 480, 240, 120}

func sampleSize(dimsamples int) int {
	for _, sr := range srates {
		if dimsamples >= sr {
			return sr
		}
	}
	return srates[len(srates)-1]
}

type Silence struct {
	raw    []byte
	ntimes int
}

func (e *Encoder) CreateSilence(d time.Duration) (*Silence, error) {
	dinsamples := int(d.Seconds() * float64(e.SamplingRate))
	onen := sampleSize(dinsamples)
	buf := make([]float32, onen*e.Channels)
	out := make([]byte, 0xFF)
	rc := C.opus_encode_float(e.ctx,
		(*C.float)(unsafe.Pointer(&buf[0])),
		C.int(onen),
		(*C.uchar)(unsafe.Pointer(&out[0])),
		C.opus_int32(len(buf)))
	if rc < 0 {
		return nil, errors.New("Invalid encoding")
	}
	out = out[:int(rc)]
	return &Silence{out, dinsamples / onen}, nil
}
func (e *Encoder) EncodeSilence(s *Silence) ([]byte, error) {
	hlen := 27 + (s.ntimes * ((len(s.raw) + 0xFE) / 0xFF))
	out := make([]byte, hlen+(s.ntimes*len(s.raw)))
	oggSplatHeader(out, e.psn, 0)
	out[26] = byte(hlen - 27)
	idx := 27
	for i := 0; i < s.ntimes; i++ {
		datalen := len(s.raw)
		for ; datalen > 0xFF; idx++ {
			out[idx] = 0xFF
			datalen -= 0xFF
		}
		out[idx] = byte(datalen)
		idx++
	}
	e.psn++
	for i := 0; i < s.ntimes; i++ {
		copy(out[idx:], s.raw)
		idx += len(s.raw)
	}
	oggChecksum(out)
	return out, nil
}

func (e *Encoder) EncodeRaw(input []byte) ([]byte, error) {
	// length is frame_size*channels*sizeof(opus_int16)
	return unsafeEncode(e, unsafe.Pointer(&input[0]), len(input)/(2*e.Channels))
}

func (e *Encoder) EncodeInt16(input []int16) ([]byte, error) {
	return unsafeEncode(e, unsafe.Pointer(&input[0]), len(input)/e.Channels)
}

func (e *Encoder) EncodeFloat(input []float32) ([]byte, error) {
	rawinput := unsafe.Pointer(&input[0])
	frames := len(input) / e.Channels
	all, buf := encoderGetBufForRaw(e)
	rc := C.opus_encode_float(e.ctx,
		(*C.float)(rawinput),
		C.int(frames),
		(*C.uchar)(unsafe.Pointer(&buf[0])),
		C.opus_int32(len(buf)))
	return encoderWriteBuf(e, all, rc)
}

const oggBufFrameOffset = 0x200

func unsafeEncode(e *Encoder, input unsafe.Pointer, frames int) ([]byte, error) {
	all, buf := encoderGetBufForRaw(e)
	rc := C.opus_encode(e.ctx,
		(*C.opus_int16)(input),
		C.int(frames),
		(*C.uchar)(unsafe.Pointer(&buf[0])),
		C.opus_int32(len(buf)))
	return encoderWriteBuf(e, all, rc)
}

func encoderGetBufForRaw(e *Encoder) (whole []byte, partial []byte) {
	whole = make([]byte, 4096)
	switch e.Framing {
	case FRAMING_OGG:
		partial = whole[oggBufFrameOffset:]
	default:
		partial = whole
	}
	return
}

func encoderWriteBuf(e *Encoder, all []byte, rc C.opus_int32) ([]byte, error) {
	if rc < 0 {
		return nil, errors.New("Opus encoding error")
	}

	var buf []byte
	switch e.Framing {
	case FRAMING_NONE:
		buf = buf[:int(rc)]
	case FRAMING_OGG:
		hlen := 27 + ((int(rc) + 0xFE) / 0xFF)
		//		log.Println("Produced packet: raw len", len(buf), "ogg header", hlen, "total?", len(buf)+hlen)
		if hlen > oggBufFrameOffset {
			return nil, errors.New("Ogg too large header size calculated")
		}
		hoff := oggBufFrameOffset - hlen
		oggSplatHeader(all[hoff:], e.psn, int(rc))
		e.psn++
		buf = all[hoff : oggBufFrameOffset+int(rc)]
		oggChecksum(buf)
		//		log.Println("=>", len(buf))
		//		log.Println(" 0 1 2 3 4 5 6 7 8 9101112131415161718192021222324252627282930313233343536373839")
		//		log.Printf("%X\n", buf[:hlen])
		//		log.Println(" 0 1 2 3 4 5 6 7 8 9101112131415161718192021222324252627282930313233343536373839")
		//		log.Printf("%X\n", buf[hlen:])
	default:
		return nil, errors.New("Opus unknown framing")
	}

	return buf, nil
}
func oggSplatHeader(out []byte, psn uint32, datalen int) {
	copy(out, oggHeaderZero)
	binary.LittleEndian.PutUint32(out[18:], psn)
	out[26] = byte((datalen + 0xFE) / 0xFF)
	i := 27
	for ; datalen > 0xFF; i++ {
		out[i] = 0xFF
		datalen -= 0xFF
	}
	out[i] = byte(datalen)
}

var oggHeaderZero = []byte{'O', 'g', 'g', 'S',
	0, 0,
	0, 0, 0, 0, 0, 0, 0, 0,
	0x11, 0x22, 0x33, 0x44,
	0, 0, 0, 0,
}

func (e *Encoder) StreamHeader() []byte {
	switch e.Framing {
	case FRAMING_OGG:
		bs := []byte{
			'O', 'g', 'g', 'S',
			0, 2,
			0, 0, 0, 0, 0, 0, 0, 0,
			0x11, 0x22, 0x33, 0x44,
			0, 0, 0, 0,
			0, 0, 0, 0,
			1, 8 + 4 + 4 + 3,
			'O', 'p', 'u', 's', 'H', 'e', 'a', 'd',
			1, byte(e.Channels), 0, 0,
			0, 0, 0, 0,
			0, 0, 0,
			// 47 bytes, 40 bytes
			'O', 'g', 'g', 'S',
			0, 0,
			0, 0, 0, 0, 0, 0, 0, 0,
			0x11, 0x22, 0x33, 0x44,
			1, 0, 0, 0,
			0, 0, 0, 0,
			1, 8 + 4,
			'O', 'p', 'u', 's', 'T', 'a', 'g', 's',
			1, 0, 0, 0, 'x',
		}
		if len(bs) != 88 || len(bs[:47]) != 47 || len(bs[47:]) != 41 {
			panic("Invalid lengths")
		}
		oggChecksum(bs[:47])
		oggChecksum(bs[47:])
		return bs
	}
	return nil
}

func oggPageHeaderSizeFor(datalen int) int {
	return 27 + ((datalen + 0xFE) / 0xFF)
}

func oggChecksum(bs []byte) {
	binary.LittleEndian.PutUint32(bs[22:], crc32.Checksum(bs, oggCrc32))
}
