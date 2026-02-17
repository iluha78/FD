package ingest

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ResolveYouTubeURL uses yt-dlp to get the direct stream URL from a YouTube link.
func ResolveYouTubeURL(ctx context.Context, youtubeURL string) (string, error) {
	cmd := exec.CommandContext(ctx, "yt-dlp",
		"--get-url",
		"--format", "best[height<=720]",
		"--no-playlist",
		youtubeURL,
	)

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("yt-dlp failed: %w", err)
	}

	url := strings.TrimSpace(string(output))
	if url == "" {
		return "", fmt.Errorf("yt-dlp returned empty URL")
	}

	return url, nil
}
