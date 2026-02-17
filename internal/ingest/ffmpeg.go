package ingest

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"sync"
)

// FrameCallback is called for each extracted JPEG frame.
type FrameCallback func(frameData []byte) error

// FFmpegExtractor extracts JPEG frames from a video stream using FFmpeg.
type FFmpegExtractor struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	cmd    *exec.Cmd
}

// StartExtraction starts FFmpeg to extract frames at the given FPS and width.
// It calls the callback for each extracted JPEG frame.
// This function blocks until the context is cancelled or the stream ends.
func (f *FFmpegExtractor) StartExtraction(ctx context.Context, streamURL string, fps int, width int, callback FrameCallback) error {
	ctx, cancel := context.WithCancel(ctx)
	f.mu.Lock()
	f.cancel = cancel
	f.mu.Unlock()

	defer cancel()

	args := []string{
		"-hide_banner",
		"-loglevel", "warning",
		"-rtsp_transport", "tcp",
		"-i", streamURL,
		"-vf", fmt.Sprintf("fps=%d,scale=%d:-1", fps, width),
		"-f", "image2pipe",
		"-vcodec", "mjpeg",
		"-q:v", "5",
		"pipe:1",
	}

	cmd := exec.CommandContext(ctx, "ffmpeg", args...)
	f.mu.Lock()
	f.cmd = cmd
	f.mu.Unlock()

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	// Log stderr in background
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			slog.Debug("ffmpeg", "output", scanner.Text())
		}
	}()

	// Read JPEG frames from stdout
	// JPEG frames start with FFD8 and end with FFD9
	if err := readJPEGFrames(stdout, callback); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("read frames: %w", err)
	}

	return cmd.Wait()
}

// Stop terminates the FFmpeg process.
func (f *FFmpegExtractor) Stop() {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.cancel != nil {
		f.cancel()
	}
	if f.cmd != nil && f.cmd.Process != nil {
		_ = f.cmd.Process.Kill()
	}
}

// readJPEGFrames reads a stream of concatenated JPEG images.
func readJPEGFrames(r io.Reader, callback FrameCallback) error {
	reader := bufio.NewReaderSize(r, 512*1024) // 512KB buffer

	for {
		// Find JPEG start marker: FF D8
		if err := findJPEGStart(reader); err != nil {
			return err
		}

		// Read until JPEG end marker: FF D9
		frameData, err := readUntilJPEGEnd(reader)
		if err != nil {
			return err
		}

		if len(frameData) > 0 {
			if err := callback(frameData); err != nil {
				slog.Warn("frame callback error", "error", err)
			}
		}
	}
}

func findJPEGStart(r *bufio.Reader) error {
	for {
		b, err := r.ReadByte()
		if err != nil {
			return err
		}
		if b != 0xFF {
			continue
		}
		b, err = r.ReadByte()
		if err != nil {
			return err
		}
		if b == 0xD8 {
			return nil
		}
	}
}

func readUntilJPEGEnd(r *bufio.Reader) ([]byte, error) {
	// Start with JPEG header
	data := []byte{0xFF, 0xD8}

	for {
		b, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		data = append(data, b)

		if b == 0xFF {
			next, err := r.ReadByte()
			if err != nil {
				return nil, err
			}
			data = append(data, next)
			if next == 0xD9 {
				return data, nil
			}
		}

		// Safety: max 10MB per frame
		if len(data) > 10*1024*1024 {
			return nil, fmt.Errorf("jpeg frame too large: %s bytes", strconv.Itoa(len(data)))
		}
	}
}
