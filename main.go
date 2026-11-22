package main

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"log"
	"os"
	"rtspsnap/analyse"
	"time"

	"github.com/Eyevinn/hi264/pkg/decoder"
	"github.com/Eyevinn/hi264/pkg/frame"
	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/pion/rtp"
)

var startCode = []byte{0, 0, 0, 1}

// captureH264Keyframe connects over RTSP and returns the first IDR access unit
// (preceded by SPS/PPS) as an Annex B byte stream.
func captureH264Keyframe(ctx context.Context, rtspURL string) ([]byte, error) {
	u, err := base.ParseURL(rtspURL)
	if err != nil {
		return nil, err
	}

	proto := gortsplib.ProtocolTCP
	c := gortsplib.Client{
		Scheme:   u.Scheme,
		Host:     u.Host,
		Protocol: &proto, // force TCP, avoids UDP packet loss on large keyframes
	}
	if err := c.Start(); err != nil {
		return nil, err
	}
	defer c.Close()

	desc, _, err := c.Describe(u)
	if err != nil {
		return nil, err
	}

	var forma *format.H264
	medi := desc.FindFormat(&forma)
	if medi == nil {
		return nil, fmt.Errorf("no H264 track (camera may use H265 or MJPEG)")
	}

	rtpDec, err := forma.CreateDecoder()
	if err != nil {
		return nil, err
	}
	if _, err := c.Setup(desc.BaseURL, medi, 0, 0); err != nil {
		return nil, err
	}

	result := make(chan []byte, 1)

	c.OnPacketRTP(medi, forma, func(pkt *rtp.Packet) {
		au, err := rtpDec.Decode(pkt) // error until a full access unit has arrived
		if err != nil || !h264.IsRandomAccess(au) {
			return
		}
		var annexB bytes.Buffer
		for _, nalu := range [][]byte{forma.SPS, forma.PPS} {
			if len(nalu) > 0 {
				annexB.Write(startCode)
				annexB.Write(nalu)
			}
		}
		for _, nalu := range au {
			annexB.Write(startCode)
			annexB.Write(nalu)
		}
		select {
		case result <- annexB.Bytes():
		default:
		}
	})

	if _, err := c.Play(nil); err != nil {
		return nil, err
	}

	select {
	case data := <-result:
		return data, nil
	case <-time.After(15 * time.Second):
		return nil, fmt.Errorf("timeout waiting for keyframe")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func clamp8(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v + 0.5)
}

// frameToImage converts a decoded 4:2:0 YCbCr frame to RGB, honouring the
// signalled range (limited/full) and matrix (BT.709 vs BT.601).
func frameToImage(f *frame.Frame) *image.RGBA {
	// BT.709 only when explicitly signalled; otherwise assume BT.601.
	kr, kb := 0.299, 0.114
	if f.MatrixCoefficients == 1 {
		kr, kb = 0.2126, 0.0722
	}
	kg := 1 - kr - kb

	img := image.NewRGBA(image.Rect(0, 0, f.Width, f.Height))
	for y := 0; y < f.Height; y++ {
		for x := 0; x < f.Width; x++ {
			yy := float64(f.Y[y*f.StrideY+x])
			cb := float64(f.Cb[(y/2)*f.StrideC+x/2]) - 128
			cr := float64(f.Cr[(y/2)*f.StrideC+x/2]) - 128

			if !f.VideoFullRangeFlag { // limited range: Y 16-235, C 16-240
				yy = (yy - 16) * 255 / 219
				cb = cb * 255 / 224
				cr = cr * 255 / 224
			}

			r := yy + 2*(1-kr)*cr
			b := yy + 2*(1-kb)*cb
			g := (yy - kr*r - kb*b) / kg

			img.SetRGBA(x, y, color.RGBA{clamp8(r), clamp8(g), clamp8(b), 255})
		}
	}
	return img
}

func main() {
	url := os.Getenv("RTSP_URL") // e.g. rtsp://user:pass@192.168.1.50:554/stream1
	if url == "" {
		log.Fatal("set RTSP_URL")
	}

	annexB, err := captureH264Keyframe(context.Background(), url)
	if err != nil {
		log.Fatalf("capture: %v", err)
	}

	dec := decoder.New()
	fr, err := dec.DecodeAnnexB(annexB)
	if err != nil {
		log.Fatalf("decode: %v", err)
	}

	img := frameToImage(fr)

	b, message, blankStats := analyse.IsBlank(img)
	if b == true {
		fmt.Println("message: " + message)
		fmt.Println(blankStats)
		log.Fatal("Image is Blank!")
		os.Exit(1)
	}
	os.Exit(0)

	// out, err := os.Create("snapshot.jpg")
	// if err != nil {
	// 	log.Fatal(err)
	// }
	// defer out.Close()

	// if err := jpeg.Encode(out, img, &jpeg.Options{Quality: 90}); err != nil {
	// 	log.Fatalf("jpeg: %v", err)
	// }
	// fmt.Printf("saved snapshot.jpg (%dx%d)\n", fr.Width, fr.Height)

}
