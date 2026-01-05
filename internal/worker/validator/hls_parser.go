package validator

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nzws/flux-encoder/internal/shared/logger"
	"go.uber.org/zap"
)

// HLSParser はHLSプレイリストのパーサー
type HLSParser struct {
	ffprobe *FFProbe
}

// NewHLSParser は新しいHLSParserを作成する
func NewHLSParser() *HLSParser {
	return &HLSParser{
		ffprobe: NewFFProbe(),
	}
}

// ParseAndValidate はHLSプレイリストをパース・検証する
func (p *HLSParser) ParseAndValidate(ctx context.Context, baseDir string, depth HLSValidationDepth) (*HLSInfo, error) {
	hlsInfo := &HLSInfo{}

	// マスタープレイリストまたはメディアプレイリストを探す
	masterPath := filepath.Join(baseDir, "master.m3u8")
	playlistPath := filepath.Join(baseDir, "playlist.m3u8")

	var mainPlaylist string
	if _, err := os.Stat(masterPath); err == nil {
		mainPlaylist = masterPath
		hlsInfo.MasterPlaylist = masterPath
	} else if _, err := os.Stat(playlistPath); err == nil {
		mainPlaylist = playlistPath
		hlsInfo.MasterPlaylist = playlistPath
	} else {
		// その他の.m3u8ファイルを探す
		entries, err := os.ReadDir(baseDir)
		if err != nil {
			return nil, fmt.Errorf("failed to read directory: %w", err)
		}
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".m3u8") {
				mainPlaylist = filepath.Join(baseDir, entry.Name())
				hlsInfo.MasterPlaylist = mainPlaylist
				break
			}
		}
	}

	if mainPlaylist == "" {
		return nil, fmt.Errorf("no HLS playlist found in directory: %s", baseDir)
	}

	// プレイリストの種類を判定
	isMaster, err := p.isMasterPlaylist(mainPlaylist)
	if err != nil {
		return nil, fmt.Errorf("failed to read playlist: %w", err)
	}

	if isMaster {
		// マスタープレイリストの場合
		return p.parseMasterPlaylist(ctx, baseDir, mainPlaylist, depth)
	}

	// 単一メディアプレイリストの場合
	return p.parseSingleMediaPlaylist(ctx, baseDir, mainPlaylist, depth)
}

// isMasterPlaylist はマスタープレイリストかどうかを判定する
func (p *HLSParser) isMasterPlaylist(path string) (bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer func() {
		if err := file.Close(); err != nil {
			logger.Warn("Failed to close playlist file", zap.Error(err))
		}
	}()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// #EXT-X-STREAM-INF があればマスタープレイリスト
		if strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			return true, nil
		}
		// #EXTINF があればメディアプレイリスト
		if strings.HasPrefix(line, "#EXTINF") {
			return false, nil
		}
	}

	return false, scanner.Err()
}

// parseMasterPlaylist はマスタープレイリストをパースする
func (p *HLSParser) parseMasterPlaylist(ctx context.Context, baseDir, masterPath string, depth HLSValidationDepth) (*HLSInfo, error) {
	hlsInfo := &HLSInfo{
		MasterPlaylist: masterPath,
	}

	file, err := os.Open(masterPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open master playlist: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			logger.Warn("Failed to close master playlist file", zap.Error(err))
		}
	}()

	scanner := bufio.NewScanner(file)
	var currentStreamInfo map[string]string

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		if strings.HasPrefix(line, "#EXT-X-STREAM-INF") {
			// STREAM-INF の属性をパース
			currentStreamInfo = p.parseAttributes(line)
			continue
		}

		if line == "" || strings.HasPrefix(line, "#") || currentStreamInfo == nil {
			continue
		}

		playlistInfo, segmentInfo, err := p.buildPlaylistInfo(ctx, baseDir, line, currentStreamInfo, depth)
		if err != nil {
			return nil, err
		}

		if segmentInfo != nil {
			hlsInfo.TotalSegments += segmentInfo.SegmentCount
			if segmentInfo.TargetDuration > hlsInfo.TargetDuration {
				hlsInfo.TargetDuration = segmentInfo.TargetDuration
			}
		}

		hlsInfo.Playlists = append(hlsInfo.Playlists, playlistInfo)
		currentStreamInfo = nil
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading master playlist: %w", err)
	}

	return hlsInfo, nil
}

func (p *HLSParser) buildPlaylistInfo(ctx context.Context, baseDir, line string, streamInfo map[string]string, depth HLSValidationDepth) (PlaylistInfo, *mediaPlaylistInfo, error) {
	mediaPlaylistPath := filepath.Join(baseDir, line)
	playlistInfo := PlaylistInfo{
		Path: mediaPlaylistPath,
	}

	if bandwidth, ok := streamInfo["BANDWIDTH"]; ok {
		if bw, err := strconv.ParseInt(bandwidth, 10, 64); err == nil {
			playlistInfo.Bandwidth = bw
		}
	}
	if resolution, ok := streamInfo["RESOLUTION"]; ok {
		playlistInfo.Resolution = resolution
	}
	if codecs, ok := streamInfo["CODECS"]; ok {
		playlistInfo.Codecs = strings.Trim(codecs, "\"")
	}

	if depth < HLSValidationDepthMedium {
		return playlistInfo, nil, nil
	}

	segmentInfo, err := p.parseMediaPlaylist(ctx, baseDir, mediaPlaylistPath, depth)
	if err != nil {
		return PlaylistInfo{}, nil, fmt.Errorf("failed to parse media playlist %s: %w", line, err)
	}
	playlistInfo.SegmentCount = segmentInfo.SegmentCount
	playlistInfo.Segments = segmentInfo.Segments

	return playlistInfo, segmentInfo, nil
}

// parseSingleMediaPlaylist は単一メディアプレイリストをパースする
func (p *HLSParser) parseSingleMediaPlaylist(ctx context.Context, baseDir, playlistPath string, depth HLSValidationDepth) (*HLSInfo, error) {
	hlsInfo := &HLSInfo{
		MasterPlaylist: playlistPath,
	}

	segmentInfo, err := p.parseMediaPlaylist(ctx, baseDir, playlistPath, depth)
	if err != nil {
		return nil, err
	}

	playlistInfo := PlaylistInfo{
		Path:         playlistPath,
		SegmentCount: segmentInfo.SegmentCount,
		Segments:     segmentInfo.Segments,
	}

	hlsInfo.Playlists = []PlaylistInfo{playlistInfo}
	hlsInfo.TotalSegments = segmentInfo.SegmentCount
	hlsInfo.TargetDuration = segmentInfo.TargetDuration

	return hlsInfo, nil
}

// mediaPlaylistInfo は内部的なメディアプレイリスト情報
type mediaPlaylistInfo struct {
	SegmentCount   int
	Segments       []SegmentInfo
	TargetDuration float64
}

// parseMediaPlaylist はメディアプレイリストをパースする
func (p *HLSParser) parseMediaPlaylist(ctx context.Context, baseDir, playlistPath string, depth HLSValidationDepth) (*mediaPlaylistInfo, error) {
	info := &mediaPlaylistInfo{}

	file, err := os.Open(playlistPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open media playlist: %w", err)
	}
	defer func() {
		if err := file.Close(); err != nil {
			logger.Warn("Failed to close media playlist file", zap.Error(err))
		}
	}()

	scanner := bufio.NewScanner(file)
	var currentDuration float64

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#EXT-X-TARGETDURATION") {
			p.updateTargetDuration(info, line)
			continue
		}

		if strings.HasPrefix(line, "#EXTINF") {
			currentDuration = parseSegmentDuration(line, currentDuration)
			continue
		}

		if strings.HasPrefix(line, "#") {
			continue
		}

		segment, err := p.buildSegmentInfo(ctx, playlistPath, line, currentDuration, depth)
		if err != nil {
			return nil, err
		}

		info.Segments = append(info.Segments, segment)
		info.SegmentCount++
		currentDuration = 0
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading media playlist: %w", err)
	}

	return info, nil
}

func (p *HLSParser) updateTargetDuration(info *mediaPlaylistInfo, line string) {
	parts := strings.Split(line, ":")
	if len(parts) != 2 {
		return
	}
	if duration, err := strconv.ParseFloat(parts[1], 64); err == nil {
		info.TargetDuration = duration
	}
}

func parseSegmentDuration(line string, fallback float64) float64 {
	parts := strings.Split(line, ":")
	if len(parts) != 2 {
		return fallback
	}

	durationStr := strings.TrimSuffix(strings.Split(parts[1], ",")[0], ",")
	duration, err := strconv.ParseFloat(durationStr, 64)
	if err != nil {
		return fallback
	}
	return duration
}

func (p *HLSParser) buildSegmentInfo(ctx context.Context, playlistPath, segmentLine string, duration float64, depth HLSValidationDepth) (SegmentInfo, error) {
	segmentPath := filepath.Join(filepath.Dir(playlistPath), segmentLine)
	if _, err := os.Stat(segmentPath); err != nil {
		return SegmentInfo{}, fmt.Errorf("segment file not found: %s", segmentPath)
	}

	segment := SegmentInfo{
		Path:     segmentPath,
		Duration: duration,
	}

	if fileInfo, err := os.Stat(segmentPath); err == nil {
		segment.Size = fileInfo.Size()
	}

	if depth >= HLSValidationDepthFull {
		segInfo, err := p.ffprobe.GetSegmentInfo(ctx, segmentPath)
		if err != nil {
			return SegmentInfo{}, fmt.Errorf("failed to validate segment %s: %w", segmentLine, err)
		}
		segment.Duration = segInfo.Duration
	}

	return segment, nil
}

// parseAttributes は属性行（例: #EXT-X-STREAM-INF:BANDWIDTH=2800000,RESOLUTION=1280x720）をパースする
func (p *HLSParser) parseAttributes(line string) map[string]string {
	attributes := make(map[string]string)

	// ":" の後の部分を取得
	parts := strings.SplitN(line, ":", 2)
	if len(parts) != 2 {
		return attributes
	}

	attrString := parts[1]
	currentKey := ""
	currentValue := ""
	inQuotes := false

	for i := 0; i < len(attrString); i++ {
		char := attrString[i]

		switch char {
		case '=':
			if !inQuotes && currentKey == "" {
				currentKey = currentValue
				currentValue = ""
			} else {
				currentValue += string(char)
			}
		case ',':
			if !inQuotes {
				if currentKey != "" {
					attributes[currentKey] = currentValue
					currentKey = ""
					currentValue = ""
				}
			} else {
				currentValue += string(char)
			}
		case '"':
			inQuotes = !inQuotes
			currentValue += string(char)
		default:
			currentValue += string(char)
		}
	}

	// 最後の属性を追加
	if currentKey != "" {
		attributes[currentKey] = currentValue
	}

	return attributes
}

// ValidatePlaylistSyntax はプレイリストの構文を検証する
func (p *HLSParser) ValidatePlaylistSyntax(content string) []ValidationError {
	var errors []ValidationError

	lines := strings.Split(content, "\n")
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "#EXTM3U") {
		errors = append(errors, ValidationError{
			Code:    "HLS_INVALID_HEADER",
			Message: "playlist must start with #EXTM3U",
		})
	}

	return errors
}
