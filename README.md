# tingting-audio-v3

HSK 3.0 example-sentence audio for the Tingting app, regenerated for the flat
`example_sentences` schema (see `scripts/hsk3/relevel-sentences.py` in the
tingting repo).

Layout: `hsk{N}/{stem}_{idx}_{variant}.mp3`, where the word's `audio_id` in the
tingting data is `hsk{N}_{stem}` (`{word}`, or `{word}_no{no}` for a word that
appears twice in a level). Levels follow the 2025 HSK 3.0 syllabus
(`scripts/hsk3/relevel-2025.py`, files moved by
`scripts/hsk3/rename-v3-sentence-audio.py`).
- `idx` 0/1/2 = the three level-appropriate sentences per word
- `variant` = `normal` (continuous) · `segmented` (word-paused) · `en` (English)

Served on GitHub Pages: `https://fbngrm.github.io/tingting-audio-v3/hsk{N}/...`

Voices (edge-tts): per word, `zh-CN-XiaoxiaoNeural` (f) or `zh-CN-YunxiNeural` (m);
index 0 of existing words is the reused HSK 2.0 clip (female). English uses the
English voice. Generator: `tools/tingting-sentences-v3.go` (depends on the
zh-audio `pkg/audio`), run with edge-tts 7.3.1 or newer. hsk1-6 are complete;
hsk7 has clips for 376 words only.
