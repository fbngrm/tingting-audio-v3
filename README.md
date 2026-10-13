# tingting-audio-v3

HSK 3.0 example-sentence audio for the Tingting app, regenerated for the flat
`example_sentences` schema (see `scripts/hsk3/relevel-sentences.py` in the
tingting repo).

Layout: `hsk{1..6}/{word}_{idx}_{variant}.mp3`
- `idx` 0/1/2 = the three level-appropriate sentences per word
- `variant` = `normal` (continuous) · `segmented` (word-paused) · `en` (English)

Voices (edge-tts): per word, `zh-CN-XiaoxiaoNeural` (f) or `zh-CN-YunxiNeural` (m);
index 0 of existing words is the reused HSK 2.0 clip (female). English uses the
English voice. Generator: `tools/tingting-sentences-v3.go` (depends on the
zh-audio `pkg/audio`). hsk7 not included.
