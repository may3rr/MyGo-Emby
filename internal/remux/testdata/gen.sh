#!/bin/bash
# Regenerates the test clips (needs ffmpeg with libx264, libx265). The
# tests themselves do not run ffmpeg. 10 s, 256x144, 24 fps, B-frames.
set -e
cd "$(dirname "$0")"
F=${FFMPEG:-ffmpeg}
V="-f lavfi -i testsrc2=size=256x144:rate=24:duration=10"
A="-f lavfi -i sine=frequency=440:sample_rate=48000:duration=10"
X264="-c:v libx264 -preset veryfast -crf 40 -sc_threshold 0 -pix_fmt yuv420p"
X265P="scenecut=0:log-level=error"
# MKV: H.264 (GOP 2 s, 3 B-frames) + AAC + ASS subtitles.
$F -v error -y $V $A -i subs.ass -map 0 -map 1 -map 2 $X264 -g 48 -keyint_min 48 -bf 3 \
  -c:a aac -b:a 24k -ac 1 -c:s ass -metadata:s:a:0 language=eng -metadata:s:s:0 language=chi \
  -metadata:s:s:0 title=ASS mkv_h264_aac_ass.mkv
# MKV: 10-bit HEVC (open GOP, CRA every 2.5 s, 4 B-frames) + E-AC-3 + SubRip.
$F -v error -y $V $A -i subs.srt -map 0 -map 1 -map 2 -c:v libx265 -preset veryfast -crf 40 \
  -x265-params "keyint=60:min-keyint=60:open-gop=1:bframes=4:$X265P" -pix_fmt yuv420p10le \
  -c:a eac3 -b:a 32k -ac 1 -c:s srt -metadata:s:a:0 language=jpn mkv_hevc_eac3_srt.mkv
# MP4: HEVC tagged hev1 + AAC + mov_text subtitles.
$F -v error -y $V $A -i subs.srt -map 0 -map 1 -map 2 -c:v libx265 -preset veryfast -crf 40 -tag:v hev1 \
  -x265-params "keyint=48:min-keyint=48:bframes=3:$X265P" -c:a aac -b:a 24k -ac 1 -c:s mov_text \
  -movflags +faststart mp4_hev1_aac.mp4
# MPEG-TS: H.264 + AAC, and HEVC + AC-3.
$F -v error -y $V $A -map 0 -map 1 $X264 -g 48 -keyint_min 48 -bf 3 -c:a aac -b:a 24k -ac 1 \
  -f mpegts ts_h264_aac.ts
$F -v error -y $V $A -map 0 -map 1 -c:v libx265 -preset veryfast -crf 40 \
  -x265-params "keyint=48:min-keyint=48:bframes=4:$X265P" -c:a ac3 -b:a 32k -ac 1 -f mpegts ts_hevc_ac3.ts
# MKV: 10-bit HEVC + DTS, for the optional local audio-transcode fallback.
$F -v error -y $V $A -map 0 -map 1 -c:v libx265 -preset veryfast -crf 40 \
  -x265-params "keyint=48:min-keyint=48:bframes=3:$X265P" -pix_fmt yuv420p10le \
  -c:a dts -strict -2 -b:a 768k -ac 2 mkv_hevc_dts.mkv
# The MKV without Cues is made by the tests from mkv_h264_aac_ass.mkv.
