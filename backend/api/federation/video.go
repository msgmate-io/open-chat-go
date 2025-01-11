//go:build !windows
// +build !windows

package federation

import (
	"bytes"
	"context"
	"fmt"
	"github.com/blackjack/webcam"
	"image"
	"image/jpeg"
	"log"
	"net/http"
	"runtime"
	"time"
)

type VideoServer struct {
	cam         *webcam.Webcam
	pixelFormat webcam.PixelFormat
	width       uint32
	height      uint32
}

func LinuxStartVideoServer(port int, videoDevice string) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		ws, err := NewVideoServer(videoDevice)
		if err != nil {
			log.Fatal(err)
		}
		defer ws.cam.Close()

		mux := http.NewServeMux()
		mux.HandleFunc("/video", ws.ServeHTTP)

		server := &http.Server{
			Addr:    fmt.Sprintf(":%d", port),
			Handler: mux,
		}

		go func() {
			<-ctx.Done()
			server.Shutdown(context.Background())
		}()

		fmt.Println("Starting video server at http://localhost:", port)
		if err := server.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("HTTP server error: %v", err)
		}
	}()

	return cancel
}

func NewVideoServer(videoDevice string) (*VideoServer, error) {
	// Open the camera device (usually /dev/video0 on Linux)
	cam, err := webcam.Open(videoDevice)
	if err != nil {
		return nil, fmt.Errorf("error opening camera: %v", err)
	}

	// Get supported formats
	formats := cam.GetSupportedFormats()
	fmt.Println("Supported formats:")
	for f, name := range formats {
		fmt.Printf("Format: %v, Name: %s\n", f, name)
	}

	var format webcam.PixelFormat
	var formatName string

	// Try different formats in order of preference
	formatPreference := []webcam.PixelFormat{
		//webcam.PixelFormat(0x47504A4D), // MJPEG TODO Re-uncomment
		webcam.PixelFormat(0x5650),     // YUV
		webcam.PixelFormat(0x32595559), // YUYV
		webcam.PixelFormat(0x56595559), // YUYV
	}

	for _, f := range formatPreference {
		if name, ok := formats[f]; ok {
			format = f
			formatName = name
			break
		}
	}

	if format == 0 {
		//pick any compatible format
		for f, name := range formats {
			format = f
			formatName = name
			break
		}
	}

	fmt.Printf("Selected format: %s (0x%x)\n", formatName, format)

	// Set the chosen format - force 640x480 resolution
	const targetWidth = 640
	const targetHeight = 480

	width, height, _, err := cam.SetImageFormat(format, targetWidth, targetHeight)
	if err != nil {
		return nil, fmt.Errorf("error setting format: %v", err)
	}

	// Override the returned dimensions since they appear to be incorrect
	width = targetWidth
	height = targetHeight

	fmt.Printf("Using dimensions: %dx%d\n", width, height)

	// Set up buffer size
	err = cam.SetBufferCount(4)
	if err != nil {
		return nil, fmt.Errorf("error setting buffer count: %v", err)
	}

	// Start streaming
	err = cam.StartStreaming()
	if err != nil {
		return nil, fmt.Errorf("error starting stream: %v", err)
	}

	vs := &VideoServer{
		cam:         cam,
		pixelFormat: format,
		width:       uint32(targetWidth),
		height:      uint32(targetHeight),
	}

	fmt.Printf("Created VideoServer with dimensions: %dx%d\n", vs.width, vs.height)
	return vs, nil
}

func (vs *VideoServer) convertFrameToJPEG(frame []byte) ([]byte, error) {
	fmt.Printf("Debug - VideoServer dimensions: %dx%d, format: 0x%x\n", vs.width, vs.height, vs.pixelFormat)

	switch vs.pixelFormat {
	case webcam.PixelFormat(0x47504A4D): // MJPEG
		return frame, nil

	case webcam.PixelFormat(0x32595559), // YUYV
		webcam.PixelFormat(0x56595559): // YUYV
		// Validate input dimensions
		if vs.width == 0 || vs.height == 0 || vs.width > 4096 || vs.height > 4096 {
			return nil, fmt.Errorf("invalid input dimensions: %dx%d", vs.width, vs.height)
		}

		// Use dimensions from the VideoServer
		width := int(vs.width)
		height := int(vs.height)

		fmt.Printf("Converting frame with dimensions: %dx%d (frame size: %d bytes)\n", width, height, len(frame))

		log.Printf("Original dimensions: %dx%d, Frame size: %d bytes", vs.width, vs.height, len(frame))

		// Validate frame size matches expected dimensions
		expectedSize := int(vs.width) * int(vs.height) * 2 // YUYV uses 2 bytes per pixel
		if len(frame) != expectedSize {
			log.Printf("Warning: frame size mismatch. Got %d bytes, expected %d bytes", len(frame), expectedSize)
			// Continue anyway but log the warning
		}

		log.Printf("Using dimensions: %dx%d", width, height)

		yuyv := image.NewYCbCr(image.Rect(0, 0, width, height), image.YCbCrSubsampleRatio422)

		// Process image with fixed dimensions
		for y := 0; y < height; y++ {
			for x := 0; x < width/2; x++ {
				srcIdx := (y*width + x*2) * 2
				if srcIdx+3 >= len(frame) {
					continue
				}

				yi := y*width + x*2
				ci := y*width/2 + x

				yuyv.Y[yi] = frame[srcIdx]     // Y0
				yuyv.Y[yi+1] = frame[srcIdx+2] // Y1
				yuyv.Cb[ci] = frame[srcIdx+1]  // U
				yuyv.Cr[ci] = frame[srcIdx+3]  // V
			}
		}

		// Use a fixed-size buffer
		buf := bytes.NewBuffer(make([]byte, 0, width*height))

		if err := jpeg.Encode(buf, yuyv, &jpeg.Options{Quality: 30}); err != nil {
			return nil, fmt.Errorf("error encoding JPEG: %v", err)
		}

		return buf.Bytes(), nil

	case webcam.PixelFormat(0x5650): // YUV
		return nil, fmt.Errorf("YUV format conversion not implemented yet")

	default:
		return nil, fmt.Errorf("unsupported pixel format: 0x%x", vs.pixelFormat)
	}
}

func (vs *VideoServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")

	// Different frame rates based on format
	frameDelay := time.Duration(33) * time.Millisecond // ~30 fps for MJPEG
	if vs.pixelFormat != webcam.PixelFormat(0x47504A4D) {
		//frameDelay = time.Second // 1 fps for formats requiring conversion
	}

	for {
		// Read frame
		err := vs.cam.WaitForFrame(5)
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		frame, err := vs.cam.ReadFrame()
		if err != nil {
			time.Sleep(100 * time.Millisecond)
			continue
		}

		var jpegFrame []byte
		// Only convert if not already MJPEG
		if vs.pixelFormat == webcam.PixelFormat(0x47504A4D) { // MJPEG
			jpegFrame = frame
		} else {
			// Convert frame for other formats
			jpegFrame, err = vs.convertFrameToJPEG(frame)
			if err != nil {
				log.Printf("error converting frame: %v", err)
				continue
			}
			runtime.GC() // Force GC after conversion
		}

		// Write frame
		if _, err := w.Write([]byte("--frame\r\n")); err != nil {
			return
		}
		if _, err := w.Write([]byte("Content-Type: image/jpeg\r\n\r\n")); err != nil {
			return
		}
		if _, err := w.Write(jpegFrame); err != nil {
			return
		}
		if _, err := w.Write([]byte("\r\n")); err != nil {
			return
		}

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		time.Sleep(frameDelay)
	}
}

func StartVideoServer(videoDevice string) error {
	vs, err := NewVideoServer(videoDevice)
	if err != nil {
		return err
	}
	defer vs.cam.Close()

	// Start HTTP server
	http.Handle("/stream", vs)
	return http.ListenAndServe(":8080", nil)
}
