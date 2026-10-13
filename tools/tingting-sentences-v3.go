// Command tingting-sentences-v3 generates audio for the tingting app's HSK
// example sentences after the HSK 3.0 re-level (scripts/hsk3/relevel-sentences.py),
// which collapsed the old simple/intermediate/advanced tiers into a flat
// example_sentences list. It is the flat-schema counterpart of
// tingting-sentences and keeps the exact same voice and settings.
//
// For each example sentence (index i in the flat list) per word it generates:
//   - {stem}_normal.mp3      Chinese, continuous (no word pauses)
//   - {stem}_segmented.mp3   Chinese, paused at each word boundary
//   - {stem}_en.mp3          English translation, continuous
// where stem is "{word}_{i}" (or "{word}_no{No}_{i}" for true duplicate-word
// entries), matching the app's flat audio key "{audioId}_{idx}_{variant}".
//
// Usage:
//
//	go run ./cmd/tingting-sentences-v3 -data /abs/path/tingting/data -out /abs/path/tingting-audio/sentences/v3 -level hsk1 -limit 10
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"hash/fnv"
	"io"
	"log"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fbngrm/zh-audio/pkg/audio"
)

// Random pause before each edge-tts request so we don't hammer the free
// (undocumented rate-limit) Bing read-aloud endpoint. Set from flags.
var minDelayMs, maxDelayMs int

func politeDelay() {
	if maxDelayMs <= 0 {
		return
	}
	d := minDelayMs
	if maxDelayMs > minDelayMs {
		d += rand.Intn(maxDelayMs - minDelayMs + 1)
	}
	time.Sleep(time.Duration(d) * time.Millisecond)
}

// The Chinese sentence audio uses the same female/male pair as the word audio
// (tingting/scripts/hsk3/generate-word-audio.py): one voice per word, shared
// by that word's sentences. The voice is a deterministic hash of the word so a
// re-run (e.g. to fill in rate-limited misses) keeps a word's clips on one
// voice. English keeps the tts client's English voice.
var zhVoices = []string{"zh-CN-XiaoxiaoNeural", "zh-CN-YunxiNeural"}

func voiceForWord(word string) string {
	h := fnv.New32a()
	h.Write([]byte(word))
	return zhVoices[h.Sum32()%uint32(len(zhVoices))]
}

var hskLevels = []string{"hsk1", "hsk2", "hsk3", "hsk4", "hsk5", "hsk6", "hsk7"}

var ignoreChars = []string{"!", "！", "？", "?", "，", ",", ".", "。", "", " ", "、"}

type sentenceItem struct {
	Sentence    string `json:"sentence"`
	Translation string `json:"translation"`
}

type wordEntry struct {
	No               int            `json:"no"`
	Word             string         `json:"word"`
	Hsk3New          bool           `json:"hsk3_new"`
	ExampleSentences []sentenceItem `json:"example_sentences"`
}

type job struct {
	level       string
	stem        string // e.g. "爱_0"
	sentence    string
	translation string
	voice       string // Chinese voice for this word's sentences
	outDir      string
}

func main() {
	dataDir := flag.String("data", "../tingting/data", "tingting data dir containing hsk{N}.json")
	outDir := flag.String("out", "../tingting-audio/sentences/v3", "output dir, organized by level subdir")
	level := flag.String("level", "", "only this level, e.g. hsk1 (default: all)")
	workers := flag.Int("workers", 8, "concurrent workers")
	limit := flag.Int("limit", 0, "limit to first N words per level, 0 = no limit (for testing)")
	force := flag.Bool("force", false, "regenerate even if output files already exist")
	scratch := flag.String("scratch", "./out/tingting-sentences-v3-scratch", "scratch dir for the tts client (wiped on start; must be on the same filesystem as -out)")
	minDelay := flag.Int("min-delay", 500, "minimum random pause before each edge-tts request, ms")
	maxDelay := flag.Int("max-delay", 2000, "maximum random pause before each edge-tts request, ms (0 disables)")
	flag.Parse()

	minDelayMs, maxDelayMs = *minDelay, *maxDelay

	tts, err := audio.NewTTSClient(*scratch, ignoreChars, true)
	if err != nil {
		log.Fatal(err)
	}

	levels := hskLevels
	if *level != "" {
		levels = []string{*level}
	}

	for _, lvl := range levels {
		if err := processLevel(tts, *dataDir, *outDir, lvl, *workers, *limit, *force); err != nil {
			log.Printf("%s: %v", lvl, err)
		}
	}
}

func processLevel(tts *audio.TTSClient, dataDir, outDir, level string, workers, limit int, force bool) error {
	path := filepath.Join(dataDir, level+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			fmt.Printf("%s: data file not found, skipping\n", level)
			return nil
		}
		return err
	}
	var words []wordEntry
	if err := json.Unmarshal(raw, &words); err != nil {
		return fmt.Errorf("parsing %s: %w", path, err)
	}

	levelOutDir := filepath.Join(outDir, level)
	if err := os.MkdirAll(levelOutDir, os.ModePerm); err != nil {
		return err
	}

	// True duplicate-word entries (e.g. 重 zhòng vs chóng) share "word" text;
	// disambiguate their stems with the unique "no" field, but only when the
	// word actually repeats so the common case keeps its plain filenames.
	wordCounts := map[string]int{}
	for _, w := range words {
		if w.Word != "" {
			wordCounts[w.Word]++
		}
	}

	var jobs []job
	count := 0
	for _, w := range words {
		if w.Word == "" {
			continue
		}
		if limit > 0 && count >= limit {
			break
		}
		count++
		stemWord := w.Word
		if wordCounts[w.Word] > 1 {
			stemWord = fmt.Sprintf("%s_no%d", w.Word, w.No)
		}
		voice := voiceForWord(w.Word)
		for i, item := range w.ExampleSentences {
			if item.Sentence == "" {
				continue
			}
			// Existing (non-new) words keep their HSK 2.0 first-sentence
			// audio (index 0), already wired in the app; only their 2 new
			// sentences (index 1, 2) need generating. New words get all 3.
			if i == 0 && !w.Hsk3New {
				continue
			}
			jobs = append(jobs, job{
				level:       level,
				stem:        fmt.Sprintf("%s_%d", stemWord, i),
				sentence:    item.Sentence,
				translation: item.Translation,
				voice:       voice,
				outDir:      levelOutDir,
			})
		}
	}

	pending := jobs[:0:0]
	for _, j := range jobs {
		if force || !allVariantsExist(j) {
			pending = append(pending, j)
		}
	}

	fmt.Printf("\n%s: %d words, %d sentence-units, %d already done, %d to generate\n",
		level, count, len(jobs), len(jobs)-len(pending), len(pending))
	if len(pending) == 0 {
		return nil
	}

	var done, failed int64
	var mu sync.Mutex
	var failures []string

	jobCh := make(chan job)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobCh {
				if err := processJob(tts, j, force); err != nil {
					atomic.AddInt64(&failed, 1)
					mu.Lock()
					failures = append(failures, fmt.Sprintf("%s: %v", j.stem, err))
					mu.Unlock()
					fmt.Printf("\n  FAILED [%s]: %v\n", j.stem, err)
				} else {
					atomic.AddInt64(&done, 1)
				}
				fmt.Printf("\r  %d/%d  done=%d  failed=%d  last=%-20s",
					atomic.LoadInt64(&done)+atomic.LoadInt64(&failed), len(pending),
					atomic.LoadInt64(&done), atomic.LoadInt64(&failed), j.stem)
			}
		}()
	}
	for _, j := range pending {
		jobCh <- j
	}
	close(jobCh)
	wg.Wait()
	fmt.Println()

	for _, f := range failures {
		fmt.Printf("  FAILED %s\n", f)
	}
	return nil
}

func variantPaths(j job) (normal, segmented, en string) {
	return filepath.Join(j.outDir, j.stem+"_normal.mp3"),
		filepath.Join(j.outDir, j.stem+"_segmented.mp3"),
		filepath.Join(j.outDir, j.stem+"_en.mp3")
}

func allVariantsExist(j job) bool {
	normal, segmented, en := variantPaths(j)
	return exists(normal) && exists(segmented) && (j.translation == "" || exists(en))
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func processJob(tts *audio.TTSClient, j job, force bool) error {
	ctx := context.Background()
	normalPath, segmentedPath, enPath := variantPaths(j)

	stripped := strings.ReplaceAll(j.sentence, " ", "")

	if force || !exists(normalPath) {
		politeDelay()
		if err := fetchTo(ctx, tts, tts.PrepareQueryRaw(stripped, j.voice, "0ms"), j.level, j.stem+"_normal", normalPath); err != nil {
			return fmt.Errorf("normal: %w", err)
		}
	}
	if force || !exists(segmentedPath) {
		politeDelay()
		if err := fetchTo(ctx, tts, tts.PrepareQueryRaw(j.sentence, j.voice, "0ms"), j.level, j.stem+"_segmented", segmentedPath); err != nil {
			return fmt.Errorf("segmented: %w", err)
		}
	}
	if j.translation != "" && (force || !exists(enPath)) {
		politeDelay()
		if err := fetchTo(ctx, tts, tts.PrepareEnglishQuery(j.translation, "0ms"), j.level, j.stem+"_en", enPath); err != nil {
			return fmt.Errorf("en: %w", err)
		}
	}
	return nil
}

// fetchTo synthesizes query via the TTS client (which always writes into its
// own flat AudioDir) and moves the result to the real destination path.
func fetchTo(ctx context.Context, tts *audio.TTSClient, query, level, uniqueName, destPath string) error {
	scratchName := level + "_" + uniqueName + "_" + strconv.FormatInt(int64(os.Getpid()), 36) + ".mp3"
	tmpPath, err := tts.Fetch(ctx, query, scratchName)
	if err != nil {
		return err
	}
	if tmpPath == "" {
		return fmt.Errorf("empty result (query filtered as ignored char?)")
	}
	if err := moveFile(tmpPath, destPath); err != nil {
		return err
	}
	return nil
}

func moveFile(src, dst string) error {
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()
	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	os.Remove(src)
	return nil
}
