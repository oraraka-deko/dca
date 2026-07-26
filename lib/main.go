package main

/*
#include <stdint.h>
#include <stdlib.h>

typedef void (*DartCallback)(const char* result_json, int32_t status);

static inline void call_dart_callback(DartCallback cb, const char* result, int32_t status) {
    if (cb != NULL) {
        cb(result, status);
    }
}
*/
import "C"
import (
	ttar "archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/fluxcd/pkg/tar"
	"github.com/gen2brain/go-fitz"
	"github.com/gen2brain/go-unarr"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/ninehills/pdf2md/pkg/pdf"
	"github.com/spf13/afero"

	"dca/utils"
)

// ---------------------------------------------------------------------------
//  VFS Engine Types (unchanged from file engine)
// ---------------------------------------------------------------------------

type MediaProgress struct {
	TotalCandidates int     `json:"total_candidates"`
	Processed       int     `json:"processed"`
	Progress        float64 `json:"progress"`
	CurrentStatus   string  `json:"current_status"`
}

type MoviePayload struct {
	Path       string       `json:"path"`
	ParentPath string       `json:"parent_path"`
	Name       string       `json:"name"`
	MovieItems []*MovieItem `json:"movie_items"`
}

type MovieItem struct {
	Path       string `json:"path"`
	ParentPath string `json:"parent_path"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	ModTime    int64  `json:"mod_time"`
}

type TvSeriesPayload struct {
	Path       string           `json:"path"`
	ParentPath string           `json:"parent_path"`
	Name       string           `json:"name"`
	Seasons    []*SeasonPayload `json:"seasons"`
}

type SeasonPayload struct {
	SeasonNumber int               `json:"season_number"`
	Episodes     []*EpisodePayload `json:"episodes"`
}

type EpisodePayload struct {
	Path          string `json:"path"`
	ParentPath    string `json:"parent_path"`
	Name          string `json:"name"`
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	Size          int64  `json:"size"`
}

type MusicAlbumPayload struct {
	Path       string          `json:"path"`
	ParentPath string          `json:"parent_path"`
	Name       string          `json:"name"`
	Tracks     []*TrackPayload `json:"tracks"`
}

type TrackPayload struct {
	Path       string `json:"path"`
	ParentPath string `json:"parent_path"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
}

type ScanResultBatch struct {
	Movies []*MoviePayload      `json:"movies,omitempty"`
	TV     []*TvSeriesPayload   `json:"tv,omitempty"`
	Music  []*MusicAlbumPayload `json:"music,omitempty"`
}

type FileNode struct {
	ID            string `json:"id"`
	RelPath       string `json:"rel_path"`
	Size          int64  `json:"size"`
	ModTime       int64  `json:"mod_time"`
	IsDir         bool   `json:"is_dir"`
	MimeType      string `json:"mime_type,omitempty"`
	HeaderHex     string `json:"header_hex,omitempty"`
	PendingAction string `json:"pending_action,omitempty"` // "CREATE_DIR", "COPY", "MOVE", "WRITE", "DELETE"
	SourcePath    string `json:"source_path,omitempty"`
	TargetDst     string `json:"target_path,omitempty"`
	NewContentHex string `json:"new_content_hex,omitempty"`

	// Archive Specific Fields
	IsArchiveMember bool   `json:"is_archive_member,omitempty"`
	ArchiveOwner    string `json:"archive_owner,omitempty"`    // RelPath of the parent archive
	ArchiveRelPath  string `json:"archive_rel_path,omitempty"` // Path inside the archive
}

type IndexDb struct {
	Version int                  `json:"version"`
	Root    string               `json:"root"`
	Files   map[string]*FileNode `json:"files"`
}

type VfsEngine struct {
	mu        sync.RWMutex
	Root      string
	DbPath    string
	Db        *IndexDb
	Sandbox   map[string]*FileNode
	InSandbox bool
}

// --- Core Helper Functions ---
type RenameResult struct {
	Before string `json:"before"`
	After  string `json:"after"`
	Status string `json:"status"` // "success", "skipped", "error"
	Error  string `json:"error,omitempty"`
}

type ParsedMedia struct {
	Name    string
	IsTv    bool
	Season  int
	Episode int
}

type RenameJob struct {
	SeqNum int
	Node   *FileNode
}

type ScanConfig struct {
	TargetDir string `json:"target_dir"`
	BatchSize int    `json:"batch_size"`
}

type RenameConfig struct {
	Target            string   `json:"target"` // "name", "ext", "both"
	ReplaceOld        string   `json:"replace_old"`
	ReplaceNew        string   `json:"replace_new"`
	Prefix            string   `json:"prefix"`
	Suffix            string   `json:"suffix"`
	Case              string   `json:"case"`    // "upper", "lower", "none"
	Pattern           string   `json:"pattern"` // e.g., "%000i_doc_%s"
	StartIndex        int      `json:"start_index"`
	StartDouble       float64  `json:"start_double"`
	DoubleStep        float64  `json:"double_step"`
	StrArgs           []string `json:"str_args"`
	Concurrency       int      `json:"concurrency"`        // Configurable CPU/Thread usage
	BatchSize         int      `json:"batch_size"`         // Configurable memory footprint
	CollisionStrategy string   `json:"collision_strategy"` // "fail", "skip", "increment"
	PreviewOnly       bool     `json:"preview_only"`
	ScanDir           string   `json:"scan_dir"`       // Root dir to scan (optional)
	ExplicitPaths     []string `json:"explicit_paths"` // Explicit list of paths (optional)
}

var (
	reInt    = regexp.MustCompile(`%0([0-9]*)i`)
	reDouble = regexp.MustCompile(`%0([0-9]*)d`)
)

var (
	videoExtensions = map[string]bool{".mp4": true, ".mkv": true, ".avi": true, ".mov": true, ".m4v": true, ".webm": true}
	audioExtensions = map[string]bool{".mp3": true, ".flac": true, ".wav": true, ".m4a": true, ".aac": true, ".ogg": true}

	// Catch S01E02, s1e2, 1x02 patterns
	tvRegex = regexp.MustCompile(`(?i)s(\d+)\s*e(\d+)|(\d+)x(\d+)`)
)

var (
	engine *VfsEngine
	fs     = afero.NewOsFs()
)

// ---------------------------------------------------------------------------
//  VFS Engine Exported Functions (unchanged)
// ---------------------------------------------------------------------------

//export ExecuteMediaScan
func ExecuteMediaScan(cConfigJson *C.char, callback C.DartCallback) {
	configJson := C.GoString(cConfigJson)

	go func() {
		var config ScanConfig
		if err := json.Unmarshal([]byte(configJson), &config); err != nil {
			sendError(callback, err)
			return
		}
		if config.BatchSize <= 0 {
			config.BatchSize = 100
		}

		RunMediaScan(config.TargetDir, config.BatchSize,
			func(p *MediaProgress) {
				payload, _ := json.Marshal(p)
				cResult := C.CString(string(payload))
				defer C.free(unsafe.Pointer(cResult))
				// status code 1 indicates standard progress tracking updates
				C.call_dart_callback(callback, cResult, 1)
			},
			func(batch *ScanResultBatch) {
				payload, _ := json.Marshal(batch)
				cResult := C.CString(string(payload))
				defer C.free(unsafe.Pointer(cResult))
				// status code 2 signals final complete parsed transactional data structure payload
				C.call_dart_callback(callback, cResult, 2)
			},
		)

		// status code 0 explicitly shuts down the channel on complete operations
		C.call_dart_callback(callback, nil, 0)
	}()
}

func parseMediaFromFilename(filename string) *ParsedMedia {
	base := filepath.Base(filename)
	ext := filepath.Ext(base)
	nameWithoutExt := strings.TrimSuffix(base, ext)

	matches := tvRegex.FindStringSubmatch(nameWithoutExt)
	if len(matches) > 0 {
		var sStr, eStr string
		if matches[1] != "" {
			sStr, eStr = matches[1], matches[2]
		} else {
			sStr, eStr = matches[3], matches[4]
		}

		var season, episode int
		fmt.Sscanf(sStr, "%d", &season)
		fmt.Sscanf(eStr, "%d", &episode)

		// Clean name by removing the trailing episode signature details
		loc := tvRegex.FindStringIndex(nameWithoutExt)
		cleanName := strings.TrimSpace(nameWithoutExt[:loc[0]])
		cleanName = strings.Trim(cleanName, "-_ .")

		return &ParsedMedia{
			Name:    cleanName,
			IsTv:    true,
			Season:  season,
			Episode: episode,
		}
	}

	return &ParsedMedia{
		Name: nameWithoutExt,
		IsTv: false,
	}
}

func RunMediaScan(rootDir string, batchSize int, progressCb func(p *MediaProgress), resultCb func(batch *ScanResultBatch)) {
	var candidates []string

	// Fast initial crawl for structural gathering
	_ = filepath.WalkDir(rootDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if videoExtensions[ext] || audioExtensions[ext] {
			candidates = append(candidates, path)
		}
		return nil
	})

	total := len(candidates)
	if total == 0 {
		return
	}

	movieMap := make(map[string]*MoviePayload)
	tvMap := make(map[string]*TvSeriesPayload)
	musicMap := make(map[string]*MusicAlbumPayload)

	for idx, path := range candidates {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}

		ext := strings.ToLower(filepath.Ext(path))
		parentPath := filepath.Dir(path)
		parentName := filepath.Base(parentPath)

		if videoExtensions[ext] {
			parsed := parseMediaFromFilename(path)
			if parsed.IsTv {
				// Process as TV Series
				series, exists := tvMap[parentPath]
				if !exists {
					series = &TvSeriesPayload{
						Path:       parentPath,
						ParentPath: filepath.Dir(parentPath),
						Name:       parsed.Name,
					}
					tvMap[parentPath] = series
				}
				if series.Name == "" {
					series.Name = parsed.Name
				}

				// Find or build appropriate Season tree
				var targetSeason *SeasonPayload
				for _, s := range series.Seasons {
					if s.SeasonNumber == parsed.Season {
						targetSeason = s
						break
					}
				}
				if targetSeason == nil {
					targetSeason = &SeasonPayload{SeasonNumber: parsed.Season}
					series.Seasons = append(series.Seasons, targetSeason)
				}

				targetSeason.Episodes = append(targetSeason.Episodes, &EpisodePayload{
					Path:          path,
					ParentPath:    parentPath,
					Name:          info.Name(),
					SeasonNumber:  parsed.Season,
					EpisodeNumber: parsed.Episode,
					Size:          info.Size(),
				})
			} else {
				// Process as Movie
				movie, exists := movieMap[parentPath]
				if !exists {
					movie = &MoviePayload{
						Path:       parentPath,
						ParentPath: filepath.Dir(parentPath),
						Name:       parentName,
					}
					movieMap[parentPath] = movie
				}
				movie.MovieItems = append(movie.MovieItems, &MovieItem{
					Path:       path,
					ParentPath: parentPath,
					Name:       info.Name(),
					Size:       info.Size(),
					ModTime:    info.ModTime().Unix(),
				})
			}
		} else if audioExtensions[ext] {
			// Process as Music Track
			album, exists := musicMap[parentPath]
			if !exists {
				album = &MusicAlbumPayload{
					Path:       parentPath,
					ParentPath: filepath.Dir(parentPath),
					Name:       parentName,
				}
				musicMap[parentPath] = album
			}
			album.Tracks = append(album.Tracks, &TrackPayload{
				Path:       path,
				ParentPath: parentPath,
				Name:       info.Name(),
				Size:       info.Size(),
			})
		}

		// Progress Streaming Tickers
		if (idx+1)%10 == 0 || idx == total-1 {
			progressCb(&MediaProgress{
				TotalCandidates: total,
				Processed:       idx + 1,
				Progress:        float64(idx+1) / float64(total),
				CurrentStatus:   "PROCESSING",
			})
		}
	}

	// Flush complete collected batches to structural callbacks
	batch := &ScanResultBatch{}
	for _, m := range movieMap {
		batch.Movies = append(batch.Movies, m)
	}
	for _, t := range tvMap {
		batch.TV = append(batch.TV, t)
	}
	for _, a := range musicMap {
		batch.Music = append(batch.Music, a)
	}
	resultCb(batch)
}

// --- Custom Pattern Formatter ---

func formatName(pattern string, index int, doubleVal float64, strArgs []string) string {
	result := pattern

	// 1. Format auto-incrementing integer (e.g., %000i -> 0066)
	result = reInt.ReplaceAllStringFunc(result, func(match string) string {
		submatches := reInt.FindStringSubmatch(match)
		width := 1
		if len(submatches) > 1 {
			digits := submatches[1]
			if digits != "" {
				allZeros := true
				for _, r := range digits {
					if r != '0' {
						allZeros = false
						break
					}
				}
				if allZeros {
					width = len(digits) + 1
				} else {
					value := 0
					fmt.Sscanf(digits, "%d", &value)
					width = value + 1
				}
			}
		}
		formatStr := fmt.Sprintf("%%0%dd", width)
		return fmt.Sprintf(formatStr, index)
	})

	// 2. Format auto-incrementing double (e.g., %000d -> 01.50)
	result = reDouble.ReplaceAllStringFunc(result, func(match string) string {
		submatches := reDouble.FindStringSubmatch(match)
		width := 1
		if len(submatches) > 1 {
			digits := submatches[1]
			if digits != "" {
				allZeros := true
				for _, r := range digits {
					if r != '0' {
						allZeros = false
						break
					}
				}
				if allZeros {
					width = len(digits) + 2
				} else {
					value := 0
					fmt.Sscanf(digits, "%d", &value)
					width = value + 2
				}
			}
		}
		formatStr := fmt.Sprintf("%%0%d.2f", width)
		return fmt.Sprintf(formatStr, doubleVal)
	})

	// 3. Format sequential string arguments (%s)
	argIdx := 0
	for strings.Contains(result, "%s") && argIdx < len(strArgs) {
		result = strings.Replace(result, "%s", strArgs[argIdx], 1)
		argIdx++
	}

	return result
}

// --- Collision Resolver ---

func resolveCollision(targetAbsPath string, strategy string) (string, string) {
	if _, err := os.Stat(targetAbsPath); os.IsNotExist(err) {
		return targetAbsPath, "success"
	}
	if strategy == "skip" {
		return targetAbsPath, "skipped"
	}
	if strategy == "fail" {
		return targetAbsPath, "error"
	}

	// Strategy: "increment" -> File (1).txt, File (2).txt
	dir := filepath.Dir(targetAbsPath)
	base := filepath.Base(targetAbsPath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	counter := 1
	for {
		newBase := fmt.Sprintf("%s (%d)%s", name, counter, ext)
		newPath := filepath.Join(dir, newBase)
		if _, err := os.Stat(newPath); os.IsNotExist(err) {
			return newPath, "success"
		}
		counter++
	}
}

// --- Single File Renaming Logic ---

func processSingleRename(node *FileNode, config *RenameConfig, index int, doubleVal float64) (string, string) {
	dir := filepath.Dir(node.RelPath)
	base := filepath.Base(node.RelPath)

	var name, ext string
	if node.IsDir {
		name = base
		ext = ""
	} else {
		ext = filepath.Ext(base)
		name = strings.TrimSuffix(base, ext)
	}

	newName := name
	newExt := ext

	// 1. Apply Pattern
	if config.Pattern != "" {
		newName = formatName(config.Pattern, index, doubleVal, config.StrArgs)
	}

	// 2. Apply Replace
	if config.ReplaceOld != "" {
		if config.Target == "name" || config.Target == "both" {
			newName = strings.ReplaceAll(newName, config.ReplaceOld, config.ReplaceNew)
		}
		if (config.Target == "ext" || config.Target == "both") && newExt != "" {
			newExt = strings.ReplaceAll(newExt, config.ReplaceOld, config.ReplaceNew)
		}
	}

	// 3. Apply Prefix / Suffix
	if config.Target == "name" || config.Target == "both" {
		newName = config.Prefix + newName + config.Suffix
	}
	if config.Target == "ext" && newExt != "" {
		dotlessExt := strings.TrimPrefix(newExt, ".")
		newExt = "." + config.Prefix + dotlessExt + config.Suffix
	}

	// 4. Apply Case Conversion
	switch strings.ToLower(config.Case) {
	case "upper":
		if config.Target == "name" || config.Target == "both" {
			newName = strings.ToUpper(newName)
		}
		if config.Target == "ext" || config.Target == "both" {
			newExt = strings.ToUpper(newExt)
		}
	case "lower":
		if config.Target == "name" || config.Target == "both" {
			newName = strings.ToLower(newName)
		}
		if config.Target == "ext" || config.Target == "both" {
			newExt = strings.ToLower(newExt)
		}
	}

	var finalBase string
	if node.IsDir {
		finalBase = newName
	} else {
		finalBase = newName + newExt
	}

	return node.RelPath, filepath.ToSlash(filepath.Join(dir, finalBase))
}

// --- Concurrent Pipeline Execution ---

//export ExecuteBatchRename
func ExecuteBatchRename(cConfigJson *C.char, callback C.DartCallback) {
	configJson := C.GoString(cConfigJson)

	go func() {
		var config RenameConfig
		if err := json.Unmarshal([]byte(configJson), &config); err != nil {
			sendError(callback, err)
			return
		}

		// Configure Concurrency & Batch limits safely
		if config.Concurrency <= 0 {
			config.Concurrency = 4
		}
		if config.BatchSize <= 0 {
			config.BatchSize = 1000
		}

		// 1. Collect Target Nodes
		var targets []*FileNode
		engine.mu.RLock()
		if len(config.ExplicitPaths) > 0 {
			for _, p := range config.ExplicitPaths {
				if node, exists := engine.Db.Files[p]; exists {
					targets = append(targets, node)
				}
			}
		} else if config.ScanDir != "" {
			scanRel := toRelPath(toAbsPath(config.ScanDir))
			for relPath, node := range engine.Db.Files {
				if relPath == scanRel || strings.HasPrefix(relPath, scanRel+"/") {
					targets = append(targets, node)
				}
			}
		}
		engine.mu.RUnlock()

		totalFiles := len(targets)
		if totalFiles == 0 {
			C.call_dart_callback(callback, nil, 0) // Finish immediately
			return
		}

		// 2. Initialize Channels
		jobsChan := make(chan RenameJob, config.BatchSize)
		resultsChan := make(chan RenameResult, config.BatchSize)
		var wg sync.WaitGroup

		// 3. Start Worker Pool
		for w := 0; w < config.Concurrency; w++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				for job := range jobsChan {
					idx := config.StartIndex + job.SeqNum
					dbl := config.StartDouble + (float64(job.SeqNum) * config.DoubleStep)

					before, after := processSingleRename(job.Node, &config, idx, dbl)
					result := RenameResult{Before: before, After: after, Status: "success"}

					if !config.PreviewOnly && before != after {
						absBefore := toAbsPath(before)
						absAfter := toAbsPath(after)

						absAfterResolved, collisionStatus := resolveCollision(absAfter, config.CollisionStrategy)
						if collisionStatus != "success" {
							result.Status = collisionStatus
							if collisionStatus == "error" {
								result.Error = "File already exists"
							}
						} else {
							err := fs.Rename(absBefore, absAfterResolved)
							if err != nil {
								result.Status = "error"
								result.Error = err.Error()
							} else {
								// Update local VFS Index in real-time
								updateVfsIndex(before, toRelPath(absAfterResolved))
							}
						}
					}
					resultsChan <- result
				}
			}()
		}

		// 4. Start Streaming Aggregator
		doneAggregator := make(chan struct{})
		go func() {
			var batch []RenameResult
			for res := range resultsChan {
				batch = append(batch, res)
				if len(batch) >= config.BatchSize {
					sendRenameBatch(callback, batch, 1) // status 1 = streaming progress
					batch = nil
				}
			}
			if len(batch) > 0 {
				sendRenameBatch(callback, batch, 1)
			}
			close(doneAggregator)
		}()

		// 5. Feed Jobs to Workers
		for i, node := range targets {
			jobsChan <- RenameJob{SeqNum: i, Node: node}
		}

		close(jobsChan)
		wg.Wait()
		close(resultsChan)
		<-doneAggregator

		// Status 0 = Complete
		C.call_dart_callback(callback, nil, 0)
	}()
}

func updateVfsIndex(oldRel, newRel string) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if node, exists := engine.Db.Files[oldRel]; exists {
		delete(engine.Db.Files, oldRel)
		node.RelPath = newRel
		engine.Db.Files[newRel] = node
	}
}

func sendRenameBatch(callback C.DartCallback, batch []RenameResult, status int32) {
	payload, _ := json.Marshal(batch)
	cResult := C.CString(string(payload))
	defer C.free(unsafe.Pointer(cResult))
	C.call_dart_callback(callback, cResult, C.int32_t(status))
}

func generateID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func toRelPath(path string) string {
	rel, err := filepath.Rel(engine.Root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func toAbsPath(relPath string) string {
	return filepath.Join(engine.Root, filepath.FromSlash(relPath))
}

func isArchiveFile(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	return ext == ".zip" || ext == ".apk" || ext == ".cbz" || ext == ".tar" || ext == ".tgz" || ext == ".gz"
}

// --- Archive Mounting Engine ---

func mountArchive(archiveRelPath string) ([]*FileNode, error) {
	absPath := toAbsPath(archiveRelPath)
	ext := strings.ToLower(filepath.Ext(absPath))

	switch ext {
	case ".zip", ".apk", ".cbz":
		return mountZip(archiveRelPath, absPath)
	case ".tar", ".tgz", ".gz":
		if strings.HasSuffix(ext, ".gz") || ext == ".tgz" {
			return mountTarGz(archiveRelPath, absPath)
		}
		return mountTar(archiveRelPath, absPath)
	}
	return nil, nil
}

func mountZip(archiveRelPath, absPath string) ([]*FileNode, error) {
	r, err := zip.OpenReader(absPath)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	var nodes []*FileNode
	for _, f := range r.File {
		archiveRel := filepath.ToSlash(f.Name)
		virtualRel := archiveRelPath + "/" + archiveRel

		nodes = append(nodes, &FileNode{
			ID:              generateID(),
			RelPath:         virtualRel,
			Size:            int64(f.UncompressedSize64),
			ModTime:         f.Modified.Unix(),
			IsDir:           f.FileInfo().IsDir(),
			IsArchiveMember: true,
			ArchiveOwner:    archiveRelPath,
			ArchiveRelPath:  archiveRel,
		})
	}
	return nodes, nil
}

func mountTar(archiveRelPath, absPath string) ([]*FileNode, error) {
	file, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var nodes []*FileNode
	tarReader := ttar.NewReader(file)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		archiveRel := filepath.ToSlash(header.Name)
		virtualRel := archiveRelPath + "/" + archiveRel

		nodes = append(nodes, &FileNode{
			ID:              generateID(),
			RelPath:         virtualRel,
			Size:            header.Size,
			ModTime:         header.ModTime.Unix(),
			IsDir:           header.Typeflag == ttar.TypeDir,
			IsArchiveMember: true,
			ArchiveOwner:    archiveRelPath,
			ArchiveRelPath:  archiveRel,
		})
	}
	return nodes, nil
}

func mountTarGz(archiveRelPath, absPath string) ([]*FileNode, error) {
	file, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	gzReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer gzReader.Close()

	var nodes []*FileNode
	tarReader := ttar.NewReader(gzReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}

		archiveRel := filepath.ToSlash(header.Name)
		virtualRel := archiveRelPath + "/" + archiveRel

		nodes = append(nodes, &FileNode{
			ID:              generateID(),
			RelPath:         virtualRel,
			Size:            header.Size,
			ModTime:         header.ModTime.Unix(),
			IsDir:           header.Typeflag == ttar.TypeDir,
			IsArchiveMember: true,
			ArchiveOwner:    archiveRelPath,
			ArchiveRelPath:  archiveRel,
		})
	}
	return nodes, nil
}

// --- Incremental Sync ---

//export IncrementalSync
func IncrementalSync(callback C.DartCallback) {
	go func() {
		engine.mu.Lock()
		defer engine.mu.Unlock()

		physicalFiles := make(map[string]bool)

		err := afero.Walk(fs, engine.Root, func(filePath string, info os.FileInfo, err error) error {
			if err != nil {
				return nil
			}
			if filePath == engine.Root || filePath == engine.DbPath {
				return nil
			}

			relPath := toRelPath(filePath)

			// If we are inside an archive member directory path, skip walking it physically
			for p := range physicalFiles {
				if isArchiveFile(p) && strings.HasPrefix(relPath, p+"/") {
					return nil
				}
			}

			physicalFiles[relPath] = true

			existing, exists := engine.Db.Files[relPath]

			if !info.IsDir() && isArchiveFile(info.Name()) {
				// Archive File detected
				archiveChanged := !exists || existing.Size != info.Size() || existing.ModTime != info.ModTime().Unix()

				if archiveChanged {
					engine.Db.Files[relPath] = &FileNode{
						ID:      generateID(),
						RelPath: relPath,
						Size:    info.Size(),
						ModTime: info.ModTime().Unix(),
						IsDir:   false,
					}
					// Mount archive members
					members, mErr := mountArchive(relPath)
					if mErr == nil {
						for _, m := range members {
							engine.Db.Files[m.RelPath] = m
							physicalFiles[m.RelPath] = true
						}
					}
				} else {
					// Archive is unchanged; mark all existing members as active
					for _, node := range engine.Db.Files {
						if node.IsArchiveMember && node.ArchiveOwner == relPath {
							physicalFiles[node.RelPath] = true
						}
					}
				}
				return nil
			}

			// Regular File/Folder handling
			if exists {
				if existing.Size != info.Size() || existing.ModTime != info.ModTime().Unix() {
					updateNodeMetadata(existing, filePath, info)
				}
			} else {
				node := &FileNode{
					ID:      generateID(),
					RelPath: relPath,
					IsDir:   info.IsDir(),
				}
				if !info.IsDir() {
					updateNodeMetadata(node, filePath, info)
				} else {
					node.ModTime = info.ModTime().Unix()
				}
				engine.Db.Files[relPath] = node
			}
			return nil
		})

		// Prune deleted items
		for relPath := range engine.Db.Files {
			if !physicalFiles[relPath] {
				delete(engine.Db.Files, relPath)
			}
		}

		if err == nil {
			_ = saveDbFile(engine.DbPath, engine.Db)
		}

		sendEngineState(callback, err)
	}()
}

// --- Content Extraction (On-Demand) ---

func readZipMember(archiveAbs, memberRel string) ([]byte, error) {
	r, err := zip.OpenReader(archiveAbs)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	for _, f := range r.File {
		if filepath.ToSlash(f.Name) == memberRel {
			rc, err := f.Open()
			if err != nil {
				return nil, err
			}
			defer rc.Close()
			return io.ReadAll(rc)
		}
	}
	return nil, os.ErrNotExist
}

func readTarMember(archiveAbs, memberRel string, isGz bool) ([]byte, error) {
	file, err := os.Open(archiveAbs)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var reader io.Reader = file
	if isGz {
		gz, err := gzip.NewReader(file)
		if err != nil {
			return nil, err
		}
		defer gz.Close()
		reader = gz
	}

	tarReader := ttar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if filepath.ToSlash(header.Name) == memberRel {
			return io.ReadAll(tarReader)
		}
	}
	return nil, os.ErrNotExist
}

// --- In-Place Archive Rebuilder & Committer ---

func rebuildZip(archiveRelPath string, callback C.DartCallback) error {
	absPath := toAbsPath(archiveRelPath)
	tempPath := absPath + ".tmp"

	var origReader *zip.ReadCloser
	origExists := false
	if _, err := os.Stat(absPath); err == nil {
		var err error
		origReader, err = zip.OpenReader(absPath)
		if err == nil {
			origExists = true
			defer origReader.Close()
		}
	}

	tempFile, err := os.Create(tempPath)
	if err != nil {
		return err
	}
	zipWriter := zip.NewWriter(tempFile)
	defer zipWriter.Close()
	defer tempFile.Close()

	// Fetch all staged nodes belonging to this archive
	var archiveNodes []*FileNode
	for _, node := range engine.Sandbox {
		if node.ArchiveOwner == archiveRelPath {
			archiveNodes = append(archiveNodes, node)
		}
	}

	total := len(archiveNodes)
	for i, node := range archiveNodes {
		// Stream progress updates to Dart (Status code 3)
		sendProgress(callback, int32((float32(i)/float32(total))*100), "Updating "+archiveRelPath)

		if node.PendingAction == "DELETE" {
			continue
		}

		if node.IsDir {
			_, _ = zipWriter.Create(node.ArchiveRelPath + "/")
			continue
		}

		writer, err := zipWriter.Create(node.ArchiveRelPath)
		if err != nil {
			return err
		}

		switch node.PendingAction {
		case "WRITE":
			data, _ := hex.DecodeString(node.NewContentHex)
			_, _ = writer.Write(data)

		case "MOVE", "COPY":
			if !node.IsArchiveMember {
				// Copying from external physical disk file
				srcFile, err := fs.Open(toAbsPath(node.SourcePath))
				if err == nil {
					_, _ = io.Copy(writer, srcFile)
					srcFile.Close()
				}
			} else {
				// Moving/Copying from within the original zip
				if origExists {
					for _, f := range origReader.File {
						if filepath.ToSlash(f.Name) == node.SourcePath {
							rc, err := f.Open()
							if err == nil {
								_, _ = io.Copy(writer, rc)
								rc.Close()
							}
							break
						}
					}
				}
			}

		default:
			// No modification: stream directly from original ZIP
			if origExists {
				for _, f := range origReader.File {
					if filepath.ToSlash(f.Name) == node.ArchiveRelPath {
						rc, err := f.Open()
						if err == nil {
							_, _ = io.Copy(writer, rc)
							rc.Close()
						}
						break
					}
				}
			}
		}
	}

	zipWriter.Close()
	tempFile.Close()
	if origExists {
		origReader.Close()
	}

	_ = os.Remove(absPath)
	return os.Rename(tempPath, absPath)
}

// --- Updated Commit Sandbox API ---

//export CommitSandbox
func CommitSandbox(callback C.DartCallback) {
	go func() {
		engine.mu.Lock()
		defer engine.mu.Unlock()
		if !engine.InSandbox {
			sendEngineState(callback, os.ErrInvalid)
			return
		}

		// Group archive modifications by parent archive
		rebuildArchives := make(map[string]bool)
		for _, node := range engine.Sandbox {
			if node.IsArchiveMember && node.PendingAction != "" {
				// Ensure the parent archive itself hasn't been deleted
				if parent, exists := engine.Sandbox[node.ArchiveOwner]; !exists || parent.PendingAction != "DELETE" {
					rebuildArchives[node.ArchiveOwner] = true
				}
			}
		}

		// Rebuild ZIP files in-place
		for archiveOwner := range rebuildArchives {
			ext := strings.ToLower(filepath.Ext(archiveOwner))
			if ext == ".zip" || ext == ".apk" || ext == ".cbz" {
				err := rebuildZip(archiveOwner, callback)
				if err != nil {
					sendError(callback, err)
					return
				}
			} else {
				sendError(callback, fmt.Errorf("archive format %s is read-only", ext))
				return
			}
		}

		// Handle normal physical operations
		var dirsToCreate []string
		var copies []*FileNode
		var moves []*FileNode
		var writes []*FileNode
		var deletes []string

		for _, node := range engine.Sandbox {
			if node.IsArchiveMember {
				continue // Handled by rebuild step
			}
			switch node.PendingAction {
			case "CREATE_DIR":
				dirsToCreate = append(dirsToCreate, node.RelPath)
			case "COPY":
				copies = append(copies, node)
			case "MOVE":
				moves = append(moves, node)
			case "WRITE":
				writes = append(writes, node)
			case "DELETE":
				deletes = append(deletes, node.RelPath)
			}
		}

		for _, dir := range dirsToCreate {
			_ = fs.MkdirAll(toAbsPath(dir), 0755)
		}
		for _, cp := range copies {
			_ = copyFilePhysical(toAbsPath(cp.SourcePath), toAbsPath(cp.RelPath))
		}
		for _, mv := range moves {
			_ = fs.Rename(toAbsPath(mv.SourcePath), toAbsPath(mv.TargetDst))
		}
		for _, wr := range writes {
			data, _ := hex.DecodeString(wr.NewContentHex)
			_ = afero.WriteFile(fs, toAbsPath(wr.RelPath), data, 0644)
		}
		for _, del := range deletes {
			_ = fs.RemoveAll(toAbsPath(del))
		}

		engine.InSandbox = false
		engine.Sandbox = nil
		engine.mu.Unlock()

		// Re-run sync to align index with new disk state
		IncrementalSync(callback)
	}()
}

func sendProgress(callback C.DartCallback, progress int32, task string) {
	progressMap := map[string]interface{}{
		"progress": progress,
		"task":     task,
	}
	payload, _ := json.Marshal(progressMap)
	cResult := C.CString(string(payload))
	defer C.free(unsafe.Pointer(cResult))
	C.call_dart_callback(callback, cResult, 3) // Status 3 = Progress Update
}

func saveDbFile(dbPath string, db *IndexDb) error {
	file, err := os.Create(dbPath)
	if err != nil {
		return err
	}
	defer file.Close()

	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()

	encoder := json.NewEncoder(gzipWriter)
	return encoder.Encode(db)
}

func loadDbFile(dbPath string) (*IndexDb, error) {
	file, err := os.Open(dbPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()

	var db IndexDb
	decoder := json.NewDecoder(gzipReader)
	if err := decoder.Decode(&db); err != nil {
		return nil, err
	}
	return &db, nil
}

// --- Exported Initialization API ---

//export InitEngine
func InitEngine(cRoot *C.char, cDbPath *C.char) int32 {
	root := filepath.Clean(C.GoString(cRoot))
	dbPath := filepath.Clean(C.GoString(cDbPath))

	engine = &VfsEngine{
		Root:   root,
		DbPath: dbPath,
		Db: &IndexDb{
			Version: 1,
			Root:    root,
			Files:   make(map[string]*FileNode),
		},
	}

	// Try to load existing index file
	if _, err := os.Stat(dbPath); err == nil {
		loaded, err := loadDbFile(dbPath)
		if err == nil {
			engine.Db = loaded
			return 1 // Loaded existing
		}
	}

	return 0 // Created new empty database
}

func updateNodeMetadata(node *FileNode, absPath string, info os.FileInfo) {
	node.Size = info.Size()
	node.ModTime = info.ModTime().Unix()

	file, err := fs.Open(absPath)
	if err != nil {
		return
	}
	defer file.Close()

	buffer := make([]byte, 512)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return
	}

	if n > 0 {
		node.MimeType = http.DetectContentType(buffer[:n])
		hexLen := 32
		if n < hexLen {
			hexLen = n
		}
		node.HeaderHex = hex.EncodeToString(buffer[:hexLen])
	}
}

// --- Sandbox / Transaction Management ---

//export BeginSandbox
func BeginSandbox() {
	engine.mu.Lock()
	defer engine.mu.Unlock()

	engine.Sandbox = make(map[string]*FileNode)
	for k, v := range engine.Db.Files {
		// Deep copy existing nodes
		nodeCopy := *v
		engine.Sandbox[k] = &nodeCopy
	}
	engine.InSandbox = true
}

//export DiscardSandbox
func DiscardSandbox() {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	engine.Sandbox = nil
	engine.InSandbox = false
}

//export SandboxCreateFolder
func SandboxCreateFolder(cRelPath *C.char) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if !engine.InSandbox {
		return
	}

	relPath := filepath.ToSlash(C.GoString(cRelPath))
	engine.Sandbox[relPath] = &FileNode{
		ID:            generateID(),
		RelPath:       relPath,
		IsDir:         true,
		PendingAction: "CREATE_DIR",
	}
}

//export SandboxDelete
func SandboxDelete(cRelPath *C.char) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if !engine.InSandbox {
		return
	}

	target := filepath.ToSlash(C.GoString(cRelPath))

	// Mark targeted file/folder and all nested children as deleted
	for relPath, node := range engine.Sandbox {
		if relPath == target || strings.HasPrefix(relPath, target+"/") {
			node.PendingAction = "DELETE"
		}
	}
}

//export SandboxMove
func SandboxMove(cSrcRel *C.char, cDstRel *C.char) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if !engine.InSandbox {
		return
	}

	src := filepath.ToSlash(C.GoString(cSrcRel))
	dst := filepath.ToSlash(C.GoString(cDstRel))

	for relPath, node := range engine.Sandbox {
		if relPath == src {
			node.PendingAction = "MOVE"
			node.SourcePath = src
			node.TargetDst = dst
		} else if strings.HasPrefix(relPath, src+"/") {
			// Handle children of moved folder
			subDst := dst + strings.TrimPrefix(relPath, src)
			node.PendingAction = "MOVE"
			node.SourcePath = relPath
			node.TargetDst = subDst
		}
	}
}

//export SandboxCopy
func SandboxCopy(cSrcRel *C.char, cDstRel *C.char) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if !engine.InSandbox {
		return
	}

	src := filepath.ToSlash(C.GoString(cSrcRel))
	dst := filepath.ToSlash(C.GoString(cDstRel))

	var copies []*FileNode
	for relPath, node := range engine.Sandbox {
		if relPath == src {
			copies = append(copies, &FileNode{
				ID:            generateID(),
				RelPath:       dst,
				IsDir:         node.IsDir,
				MimeType:      node.MimeType,
				HeaderHex:     node.HeaderHex,
				PendingAction: "COPY",
				SourcePath:    src,
			})
		} else if strings.HasPrefix(relPath, src+"/") {
			subDst := dst + strings.TrimPrefix(relPath, src)
			copies = append(copies, &FileNode{
				ID:            generateID(),
				RelPath:       subDst,
				IsDir:         node.IsDir,
				MimeType:      node.MimeType,
				HeaderHex:     node.HeaderHex,
				PendingAction: "COPY",
				SourcePath:    relPath,
			})
		}
	}

	for _, cp := range copies {
		engine.Sandbox[cp.RelPath] = cp
	}
}

//export SandboxWriteContent
func SandboxWriteContent(cRelPath *C.char, cContentHex *C.char) {
	engine.mu.Lock()
	defer engine.mu.Unlock()
	if !engine.InSandbox {
		return
	}

	relPath := filepath.ToSlash(C.GoString(cRelPath))
	contentHex := C.GoString(cContentHex)

	node, exists := engine.Sandbox[relPath]
	if !exists {
		node = &FileNode{
			ID:      generateID(),
			RelPath: relPath,
			IsDir:   false,
		}
		engine.Sandbox[relPath] = node
	}

	node.PendingAction = "WRITE"
	node.NewContentHex = contentHex
}

func copyFilePhysical(src, dst string) error {
	in, err := fs.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := fs.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// --- Content Access (Sandbox Aware) ---

//export GetFileContent
func GetFileContent(cRelPath *C.char, callback C.DartCallback) {
	relPath := filepath.ToSlash(C.GoString(cRelPath))

	go func() {
		engine.mu.RLock()
		defer engine.mu.RUnlock()

		// If in sandbox and file has staged content, return that
		if engine.InSandbox {
			if node, exists := engine.Sandbox[relPath]; exists && node.PendingAction == "WRITE" {
				C.call_dart_callback(callback, C.CString(node.NewContentHex), 0)
				return
			}
		}

		// Otherwise, read from physical disk
		data, err := afero.ReadFile(fs, toAbsPath(relPath))
		if err != nil {
			sendError(callback, err)
			return
		}

		hexStr := hex.EncodeToString(data)
		cResult := C.CString(hexStr)
		defer C.free(unsafe.Pointer(cResult))
		C.call_dart_callback(callback, cResult, 0)
	}()
}

// --- State Serialization ---

//export GetVirtualManifest
func GetVirtualManifest(callback C.DartCallback) {
	go func() {
		engine.mu.RLock()
		defer engine.mu.RUnlock()

		var targetMap map[string]*FileNode
		if engine.InSandbox {
			targetMap = engine.Sandbox
		} else {
			targetMap = engine.Db.Files
		}

		var list []*FileNode
		for _, node := range targetMap {
			if node.PendingAction != "DELETE" {
				list = append(list, node)
			}
		}

		payload, _ := json.Marshal(list)
		cResult := C.CString(string(payload))
		defer C.free(unsafe.Pointer(cResult))
		C.call_dart_callback(callback, cResult, 0)
	}()
}

func sendEngineState(callback C.DartCallback, err error) {
	if err != nil {
		sendError(callback, err)
		return
	}
	C.call_dart_callback(callback, nil, 0)
}

func sendError(callback C.DartCallback, err error) {
	errMap := map[string]string{"error": err.Error()}
	payload, _ := json.Marshal(errMap)
	cResult := C.CString(string(payload))
	defer C.free(unsafe.Pointer(cResult))
	C.call_dart_callback(callback, cResult, -1)
}

// ---------------------------------------------------------------------------
//  NEW: Extraction Types & Logic (refactored from extractor)
// ---------------------------------------------------------------------------

// ExtractPayload describes the command from Dart.
type ExtractPayload struct {
	SrcArchivePath string `json:"src_archive_path"` // physical path to the file
	DestFolderPath string `json:"dest_folder_path"` // exact target directory where files should land
}

// ExtractProgress is streamed back to Dart via callback (status=1).
type ExtractProgress struct {
	TotalFiles    int     `json:"total_files"`
	Processed     int     `json:"processed"`
	Progress      float64 `json:"progress"` // 0.0 to 100.0
	CurrentStatus string  `json:"current_status"`
}

// ExtractChapterNumber tries to extract a chapter number from a filename.
func ExtractChapterNumber(filename string) (string, bool) {
	re := regexp.MustCompile(`(?i)\b(?:ch|chapter)(?:[ _\-]*)([0-9]+)\b`)
	matches := re.FindStringSubmatch(filename)
	if len(matches) > 1 {
		return matches[1], true
	}
	return "", false
}

// ---------------------------------------------------------------------------
//  Helfer functions (unchanged logic, but no longer embedded logging calls)
// ---------------------------------------------------------------------------

func Unarchive(a *unarr.Archive, t string) error {
	_, err := a.Extract(t)
	return err
}

func Uncbz(source string, targetDir string) error {
	reader, err := zip.OpenReader(source)
	if err != nil {
		return fmt.Errorf("failed to open cbz: %w", err)
	}
	defer reader.Close()

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	for _, file := range reader.File {
		extractedFilePath := filepath.Join(targetDir, file.Name)
		if !strings.HasPrefix(extractedFilePath, filepath.Clean(targetDir)+string(os.PathSeparator)) {
			return fmt.Errorf("illegal file path in archive: %s", file.Name)
		}

		if file.FileInfo().IsDir() {
			os.MkdirAll(extractedFilePath, file.Mode())
			continue
		}

		if err := os.MkdirAll(filepath.Dir(extractedFilePath), 0755); err != nil {
			return err
		}

		archiveFile, err := file.Open()
		if err != nil {
			return err
		}

		destinationFile, err := os.OpenFile(extractedFilePath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, file.Mode())
		if err != nil {
			archiveFile.Close()
			return err
		}

		_, err = io.Copy(destinationFile, archiveFile)
		archiveFile.Close()
		destinationFile.Close()

		if err != nil {
			return fmt.Errorf("failed to extract file %s: %w", file.Name, err)
		}
	}
	return nil
}

func Unepub(fp, tp string) error {
	nd, err := fitz.New(fp)
	if err != nil {
		return err
	}
	defer nd.Close()

	ni := nd.NumPage()
	if err := os.MkdirAll(tp, 0755); err != nil {
		return err
	}

	for l := 0; l < ni; l++ {
		png, err := nd.ImagePNG(l, 100)
		if err != nil {
			return err
		}
		f := fmt.Sprintf("%s/%s.png", tp, strconv.Itoa(l))
		ff, err := os.Create(f)
		if err != nil {
			return err
		}
		if _, err := ff.Write(png); err != nil {
			ff.Close()
			return err
		}
		ff.Close()
	}
	return nil
}

func Unpdf(p, t string) error {
	_, err := pdf.ExtractPages(p, 100, t)
	return err
}

func Untar(p, t string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()

	if err := tar.Untar(f, t, tar.WithSkipGzip(), tar.WithMaxUntarSize(5000<<20)); err != nil {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
//  Unified extraction entry point
// ---------------------------------------------------------------------------

func sendExtractProgress(callback C.DartCallback, total, processed int, status string) {
	prog := &ExtractProgress{
		TotalFiles:    total,
		Processed:     processed,
		Progress:      float64(processed) / float64(total) * 100,
		CurrentStatus: status,
	}
	if status == "complete" {
		prog.Progress = 100.0
	}
	payload, _ := json.Marshal(prog)
	cResult := C.CString(string(payload))
	defer C.free(unsafe.Pointer(cResult))
	C.call_dart_callback(callback, cResult, 1) // status 1 = progress stream
}

//export ExtractToDestination
func ExtractToDestination(commandJson *C.char, callback C.DartCallback) {
	goJson := C.GoString(commandJson)

	go func() {
		var payload ExtractPayload
		if err := json.Unmarshal([]byte(goJson), &payload); err != nil {
			sendError(callback, fmt.Errorf("invalid payload: %w", err))
			return
		}

		src := payload.SrcArchivePath
		dst := payload.DestFolderPath

		// Ensure destination exists
		if err := os.MkdirAll(dst, 0755); err != nil {
			sendError(callback, fmt.Errorf("cannot create destination directory: %w", err))
			return
		}

		ext := strings.ToLower(filepath.Ext(src))

		switch ext {
		case ".pdf":
			sendExtractProgress(callback, 1, 0, "extracting PDF")
			if err := Unpdf(src, dst); err != nil {
				sendError(callback, fmt.Errorf("PDF extraction failed: %w", err))
				return
			}
			sendExtractProgress(callback, 1, 1, "complete")

		case ".epub":
			sendExtractProgress(callback, 1, 0, "extracting EPUB")
			if err := Unepub(src, dst); err != nil {
				sendError(callback, fmt.Errorf("EPUB extraction failed: %w", err))
				return
			}
			sendExtractProgress(callback, 1, 1, "complete")

		case ".cbz":
			sendExtractProgress(callback, 1, 0, "extracting CBZ")
			if err := Uncbz(src, dst); err != nil {
				sendError(callback, fmt.Errorf("CBZ extraction failed: %w", err))
				return
			}
			sendExtractProgress(callback, 1, 1, "complete")

		case ".tar":
			sendExtractProgress(callback, 1, 0, "extracting TAR")
			if err := Untar(src, dst); err != nil {
				sendError(callback, fmt.Errorf("TAR extraction failed: %w", err))
				return
			}
			sendExtractProgress(callback, 1, 1, "complete")

		default: // .zip, .7z, .rar
			sendExtractProgress(callback, 1, 0, "extracting archive")
			arch, err := unarr.NewArchive(src)
			if err != nil {
				sendError(callback, fmt.Errorf("cannot open archive: %w", err))
				return
			}
			if err := Unarchive(arch, dst); err != nil {
				sendError(callback, fmt.Errorf("archive extraction failed: %w", err))
				return
			}
			sendExtractProgress(callback, 1, 1, "complete")
		}

		// Final success
		C.call_dart_callback(callback, nil, 0)
	}()
}

// ---------------------------------------------------------------------------
//  Utils Exports: GitManager, SmartEditor, CodeOutline
// ---------------------------------------------------------------------------

// GitPayload is the JSON input for git operations.
type GitPayload struct {
	Path    string `json:"path"`
	Branch  string `json:"branch,omitempty"`
	Tag     string `json:"tag,omitempty"`
	Message string `json:"message,omitempty"`
	Author  string `json:"author,omitempty"`
	Email   string `json:"email,omitempty"`
	Files   []string `json:"files,omitempty"`
	Count   int    `json:"count,omitempty"`
	Hash    string `json:"hash,omitempty"`
	Label   string `json:"label,omitempty"`
	Remote  string `json:"remote,omitempty"`
	URL     string `json:"url,omitempty"`
	Target  string `json:"target,omitempty"`
}

// GitResult is the JSON output for git operations.
type GitResult struct {
	Success bool        `json:"success"`
	Data    interface{} `json:"data,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// ---------------------------------------------------------------------------
//  GitManager Exports
// ---------------------------------------------------------------------------

//export GitOpen
func GitOpen(cPath *C.char, callback C.DartCallback) {
	path := C.GoString(cPath)

	go func() {
		gm, err := openGitManager(path)
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{
			"path":   gm.Path(),
			"is_bare": fmt.Sprintf("%v", gm.IsBare()),
		})
	}()
}

//export GitInit
func GitInit(cPath *C.char, cBare *C.char, callback C.DartCallback) {
	path := C.GoString(cPath)
	bare := C.GoString(cBare) == "true"

	go func() {
		gm, err := initGit(path, bare)
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{
			"path":    gm.Path(),
			"is_bare": fmt.Sprintf("%v", gm.IsBare()),
		})
	}()
}

//export GitClone
func GitClone(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			URL   string `json:"url"`
			Path  string `json:"path"`
			Depth int    `json:"depth"`
			Bare  bool   `json:"bare"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := cloneGit(payload.URL, payload.Path, payload.Depth, payload.Bare)
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"path": gm.Path()})
	}()
}

//export GitStatus
func GitStatus(cPath *C.char, callback C.DartCallback) {
	path := C.GoString(cPath)

	go func() {
		gm, err := openGitManager(path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		status, err := gm.Status()
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, status)
	}()
}

//export GitCompactStatus
func GitCompactStatus(cPath *C.char, callback C.DartCallback) {
	path := C.GoString(cPath)

	go func() {
		gm, err := openGitManager(path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		cs, err := gm.GetCompactStatus()
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, cs)
	}()
}

//export GitLog
func GitLog(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Path  string `json:"path"`
			Count int    `json:"count"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		commits, err := gm.Log(payload.Count)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		var result []map[string]interface{}
		for _, c := range commits {
			result = append(result, map[string]interface{}{
				"hash":    c.Hash.String(),
				"message": strings.TrimRight(c.Message, "\n"),
				"author":  c.Author.Name,
				"email":   c.Author.Email,
				"when":    c.Author.When.Format(time.RFC3339),
			})
		}
		sendGitResult(callback, result)
	}()
}

//export GitCompactLog
func GitCompactLog(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Path  string `json:"path"`
			Count int    `json:"count"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		logs, err := gm.GetCompactLog(payload.Count)
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, logs)
	}()
}

//export GitCommit
func GitCommit(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload GitPayload
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		if len(payload.Files) > 0 {
			if err := gm.Add(payload.Files...); err != nil {
				sendGitError(callback, err)
				return
			}
		}

		hash, err := gm.Commit(payload.Message, payload.Author, payload.Email)
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"hash": hash.String()})
	}()
}

//export GitAdd
func GitAdd(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Path  string   `json:"path"`
			Files []string `json:"files"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		if err := gm.Add(payload.Files...); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"staged": strings.Join(payload.Files, ", ")})
	}()
}

//export GitCheckout
func GitCheckout(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload GitPayload
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		if err := gm.Checkout(payload.Branch, nil); err != nil {
			sendGitError(callback, err)
			return
		}

		branch, _ := gm.Branch()
		sendGitResult(callback, map[string]string{"branch": branch})
	}()
}

//export GitBranch
func GitBranch(cPath *C.char, callback C.DartCallback) {
	path := C.GoString(cPath)

	go func() {
		gm, err := openGitManager(path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		branches, err := gm.Branches()
		if err != nil {
			sendGitError(callback, err)
			return
		}

		current, _ := gm.Branch()
		var result []map[string]interface{}
		for _, ref := range branches {
			result = append(result, map[string]interface{}{
				"name":    ref.Name().Short(),
				"hash":    ref.Hash().String(),
				"current": ref.Name().Short() == current,
			})
		}
		sendGitResult(callback, result)
	}()
}

//export GitCreateBranch
func GitCreateBranch(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload GitPayload
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		if payload.Target != "" {
			err = gm.CreateBranchFrom(payload.Branch, payload.Target)
		} else {
			err = gm.CreateBranch(payload.Branch)
		}
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"branch": payload.Branch})
	}()
}

//export GitDeleteBranch
func GitDeleteBranch(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Path   string `json:"path"`
			Branch string `json:"branch"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		if err := gm.DeleteBranch(payload.Branch); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"deleted": payload.Branch})
	}()
}

//export GitTags
func GitTags(cPath *C.char, callback C.DartCallback) {
	path := C.GoString(cPath)

	go func() {
		gm, err := openGitManager(path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		tags, err := gm.Tags()
		if err != nil {
			sendGitError(callback, err)
			return
		}

		var result []map[string]string
		for _, ref := range tags {
			result = append(result, map[string]string{
				"name": ref.Name().Short(),
				"hash": ref.Hash().String(),
			})
		}
		sendGitResult(callback, result)
	}()
}

//export GitCreateTag
func GitCreateTag(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload GitPayload
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		if payload.Message != "" {
			_, err = gm.CreateAnnotatedTag(payload.Tag, payload.Message, payload.Author, payload.Email)
		} else {
			err = gm.CreateTag(payload.Tag)
		}
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"tag": payload.Tag})
	}()
}

//export GitDeleteTag
func GitDeleteTag(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Path string `json:"path"`
			Tag  string `json:"tag"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		if err := gm.DeleteTag(payload.Tag); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"deleted": payload.Tag})
	}()
}

//export GitRemotes
func GitRemotes(cPath *C.char, callback C.DartCallback) {
	path := C.GoString(cPath)

	go func() {
		gm, err := openGitManager(path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		remotes, err := gm.Remotes()
		if err != nil {
			sendGitError(callback, err)
			return
		}

		var result []map[string]interface{}
		for _, remote := range remotes {
			cfg := remote.Config()
			result = append(result, map[string]interface{}{
				"name": cfg.Name,
				"urls": cfg.URLs,
			})
		}
		sendGitResult(callback, result)
	}()
}

//export GitAddRemote
func GitAddRemote(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Path   string `json:"path"`
			Name   string `json:"name"`
			URL    string `json:"url"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		if err := gm.AddRemote(payload.Name, payload.URL); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"remote": payload.Name, "url": payload.URL})
	}()
}

//export GitDeleteRemote
func GitDeleteRemote(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Path   string `json:"path"`
			Name   string `json:"name"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		if err := gm.DeleteRemote(payload.Name); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"deleted": payload.Name})
	}()
}

//export GitReset
func GitReset(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Path string   `json:"path"`
			Mode int      `json:"mode"`
			Files []string `json:"files,omitempty"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		if err := gm.Reset(&git.ResetOptions{
			Mode:  git.ResetMode(payload.Mode),
			Files: payload.Files,
		}); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"reset": "ok"})
	}()
}

//export GitCheckpoint
func GitCheckpoint(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Path  string `json:"path"`
			Label string `json:"label,omitempty"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		hash, err := gm.CreateGitCheckpoint(payload.Label)
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"checkpoint": hash})
	}()
}

//export GitReadFile
func GitReadFile(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Path     string `json:"path"`
			FilePath string `json:"file_path"`
			Branch   string `json:"branch,omitempty"`
			Hash     string `json:"hash,omitempty"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		gm, err := openGitManager(payload.Path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		var content string
		if payload.Branch != "" {
			content, err = gm.ReadFileFromBranch(payload.Branch, payload.FilePath)
		} else if payload.Hash != "" {
			h := plumbing.NewHash(payload.Hash)
			content, err = gm.ReadFileFromCommit(h, payload.FilePath)
		} else {
			content, err = gm.ReadFileFromHEAD(payload.FilePath)
		}
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{
			"file":    payload.FilePath,
			"content": content,
		})
	}()
}

//export GitIsClean
func GitIsClean(cPath *C.char, callback C.DartCallback) {
	path := C.GoString(cPath)

	go func() {
		gm, err := openGitManager(path)
		if err != nil {
			sendGitError(callback, err)
			return
		}

		clean, err := gm.IsClean()
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]bool{"clean": clean})
	}()
}

// ---------------------------------------------------------------------------
//  SmartEditor Export
// ---------------------------------------------------------------------------

//export SmartEdit
func SmartEdit(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var req utils.SmartEditRequest
		if err := json.Unmarshal([]byte(jsonStr), &req); err != nil {
			sendGitError(callback, err)
			return
		}

		resp, err := utils.ApplySmartEdit(req)
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, resp)
	}()
}

// ---------------------------------------------------------------------------
//  CodeOutline Export
// ---------------------------------------------------------------------------

//export GetCodeOutline
func GetCodeOutline(cPath *C.char, callback C.DartCallback) {
	filePath := C.GoString(cPath)

	go func() {
		symbols, err := getCodeOutlineFn(filePath)
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, symbols)
	}()
}

// ---------------------------------------------------------------------------
//  System Status Exports
// ---------------------------------------------------------------------------

//export SystemStatus
func SystemStatus(callback C.DartCallback) {
	go func() {
		status := utils.GetStatusInfoJSON()
		sendGitResult(callback, status)
	}()
}

//export SystemLogs
func SystemLogs(cCount *C.char, cLevel *C.char, callback C.DartCallback) {
	count := C.GoString(cCount)
	level := C.GoString(cLevel)

	go func() {
		logs := utils.GetLogs(count, level)
		sendGitResult(callback, logs)
	}()
}

// ---------------------------------------------------------------------------
//  Process / Service / Task Exports
// ---------------------------------------------------------------------------

//export ListProcesses
func ListProcesses(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Query  string `json:"query"`
			SortBy string `json:"sort_by"`
			Limit  int    `json:"limit"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		procs, err := utils.ListProcesses(payload.Query, payload.SortBy, payload.Limit)
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, procs)
	}()
}

//export KillProcess
func KillProcess(pid C.int32_t, callback C.DartCallback) {
	go func() {
		if err := utils.KillProcess(int32(pid)); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]interface{}{"pid": int32(pid), "killed": true})
	}()
}

//export SignalProcess
func SignalProcess(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			PID    int32  `json:"pid"`
			Signal string `json:"signal"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		if err := utils.SignalProcess(payload.PID, payload.Signal); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]interface{}{"pid": payload.PID, "signal": payload.Signal})
	}()
}

//export ListServices
func ListServices(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		svcs, err := utils.ListServices(payload.Query, payload.Limit)
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, svcs)
	}()
}

//export ManageService
func ManageService(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Name   string `json:"name"`
			Action string `json:"action"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		if err := utils.ManageService(payload.Name, payload.Action); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"name": payload.Name, "action": payload.Action})
	}()
}

//export CreateService
func CreateService(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Name        string `json:"name"`
			DisplayName string `json:"display_name"`
			BinPath     string `json:"bin_path"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		if err := utils.CreateService(payload.Name, payload.DisplayName, payload.BinPath); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"name": payload.Name, "created": "ok"})
	}()
}

//export ListScheduledTasks
func ListScheduledTasks(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Query string `json:"query"`
			Limit int    `json:"limit"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		tasks, err := utils.ListScheduledTasks(payload.Query, payload.Limit)
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, tasks)
	}()
}

//export CreateScheduledTask
func CreateScheduledTask(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Name      string `json:"name"`
			TaskRun   string `json:"task_run"`
			Schedule  string `json:"schedule"`
			StartTime string `json:"start_time"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		if err := utils.CreateScheduledTask(payload.Name, payload.TaskRun, payload.Schedule, payload.StartTime); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"name": payload.Name, "created": "ok"})
	}()
}

//export DeleteScheduledTask
func DeleteScheduledTask(cName *C.char, callback C.DartCallback) {
	name := C.GoString(cName)

	go func() {
		if err := utils.DeleteScheduledTask(name); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"name": name, "deleted": "ok"})
	}()
}

// ---------------------------------------------------------------------------
//  Syslog / Log Parsing Exports
// ---------------------------------------------------------------------------

//export ParseSyslog
func ParseSyslog(cRaw *C.char, callback C.DartCallback) {
	raw := C.GoString(cRaw)

	go func() {
		entries, err := utils.ParseSyslog(strings.NewReader(raw))
		if err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, entries)
	}()
}

//export FilterSyslogEntries
func FilterSyslogEntries(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Raw        string            `json:"raw"`
			Severities []utils.LogSeverity `json:"severities"`
			Query      string            `json:"query"`
			Limit      int               `json:"limit"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		entries, err := utils.ParseSyslog(strings.NewReader(payload.Raw))
		if err != nil {
			sendGitError(callback, err)
			return
		}

		filtered := utils.FilterEntries(entries, utils.LogFilter{
			Severities: payload.Severities,
			Query:      payload.Query,
			Limit:      payload.Limit,
		})
		sendGitResult(callback, filtered)
	}()
}

// ---------------------------------------------------------------------------
//  App Registration Exports
// ---------------------------------------------------------------------------

//export RegisterApp
func RegisterApp(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var cfg utils.AppConfig
		if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
			sendGitError(callback, err)
			return
		}

		if err := utils.RegisterPortableApp(cfg); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"app": cfg.AppName, "registered": "ok"})
	}()
}

//export RemoveApp
func RemoveApp(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var cfg utils.AppConfig
		if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
			sendGitError(callback, err)
			return
		}

		if err := utils.RemovePortableApp(cfg); err != nil {
			sendGitError(callback, err)
			return
		}
		sendGitResult(callback, map[string]string{"app": cfg.AppName, "removed": "ok"})
	}()
}

// ---------------------------------------------------------------------------
//  HTTP / REST Exports
// ---------------------------------------------------------------------------

// HTTPResponse is the JSON response wrapper for HTTP calls.
type HTTPResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	BodyBase64 string            `json:"body_base64,omitempty"`
}

// RESTClientConfig holds credentials and base URL for a named REST client.
type RESTClientConfig struct {
	Name   string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKeyName  string `json:"api_key_name,omitempty"`
	APIKeyValue string `json:"api_key_value,omitempty"`
	BearerToken string `json:"bearer_token,omitempty"`
}

var (
	restClientsMu sync.Mutex
	restClients   = make(map[string]*utils.RESTClient)
)

//export RESTClientCreate
func RESTClientCreate(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var cfg RESTClientConfig
		if err := json.Unmarshal([]byte(jsonStr), &cfg); err != nil {
			sendGitError(callback, err)
			return
		}

		var opts []utils.RESTClientOption
		if cfg.BaseURL != "" {
			opts = append(opts, utils.WithBaseURL(cfg.BaseURL))
		}
		if cfg.BearerToken != "" {
			opts = append(opts, utils.WithBearerAuth(cfg.BearerToken))
		}
		if cfg.APIKeyName != "" && cfg.APIKeyValue != "" {
			opts = append(opts, utils.WithAPIKeyAuth(cfg.APIKeyName, cfg.APIKeyValue))
		}

		client := utils.NewRESTClient(opts...)
		restClientsMu.Lock()
		restClients[cfg.Name] = client
		restClientsMu.Unlock()

		sendGitResult(callback, map[string]string{"client": cfg.Name, "created": "ok"})
	}()
}

//export RESTClientRemove
func RESTClientRemove(cName *C.char, callback C.DartCallback) {
	name := C.GoString(cName)

	go func() {
		restClientsMu.Lock()
		delete(restClients, name)
		restClientsMu.Unlock()

		sendGitResult(callback, map[string]string{"client": name, "removed": "ok"})
	}()
}

//export RESTRequest
func RESTRequest(cJson *C.char, callback C.DartCallback) {
	jsonStr := C.GoString(cJson)

	go func() {
		var payload struct {
			Client  string            `json:"client"`
			Method  string            `json:"method"`
			Path    string            `json:"path"`
			Query   map[string]string `json:"query,omitempty"`
			Headers map[string]string `json:"headers,omitempty"`
			Body    string            `json:"body,omitempty"`
		}
		if err := json.Unmarshal([]byte(jsonStr), &payload); err != nil {
			sendGitError(callback, err)
			return
		}

		restClientsMu.Lock()
		client, ok := restClients[payload.Client]
		restClientsMu.Unlock()
		if !ok {
			sendGitError(callback, fmt.Errorf("REST client %q not found", payload.Client))
			return
		}

		query := make(url.Values)
		for k, v := range payload.Query {
			query.Set(k, v)
		}

		var body interface{}
		if payload.Body != "" {
			body = strings.NewReader(payload.Body)
		}

		ctx := context.Background()
		resp, err := client.Send(ctx, strings.ToUpper(payload.Method), payload.Path, query, body, payload.Headers)
		if err != nil {
			sendGitError(callback, err)
			return
		}
		defer resp.Body.Close()

		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
		headers := make(map[string]string)
		for k, v := range resp.Header {
			if len(v) > 0 {
				headers[k] = strings.Join(v, ", ")
			}
		}

		sendGitResult(callback, &HTTPResponse{
			StatusCode: resp.StatusCode,
			Headers:    headers,
			Body:       string(respBody),
		})
	}()
}

// ---------------------------------------------------------------------------
//  Utils helper functions (internal bridge)
// ---------------------------------------------------------------------------
func openGitManager(path string) (*utils.GitManager, error) {
	return utils.NewGitManager(path)
}

func initGit(path string, bare bool) (*utils.GitManager, error) {
	return utils.InitGitManager(path, bare)
}

func cloneGit(urlStr, path string, depth int, bare bool) (*utils.GitManager, error) {
	ctx := context.Background()
	return utils.CloneGitManager(ctx, &utils.CloneOptions{
		URL:   urlStr,
		Path:  path,
		Depth: depth,
		Bare:  bare,
	})
}

func getCodeOutlineFn(filePath string) ([]utils.SymbolInfo, error) {
	return utils.GetCodeOutline(filePath)
}

func sendGitResult(callback C.DartCallback, data interface{}) {
	result := GitResult{Success: true, Data: data}
	payload, _ := json.Marshal(result)
	cResult := C.CString(string(payload))
	defer C.free(unsafe.Pointer(cResult))
	C.call_dart_callback(callback, cResult, 0)
}

func sendGitError(callback C.DartCallback, err error) {
	result := GitResult{Success: false, Error: err.Error()}
	payload, _ := json.Marshal(result)
	cResult := C.CString(string(payload))
	defer C.free(unsafe.Pointer(cResult))
	C.call_dart_callback(callback, cResult, -1)
}

func main() {}
