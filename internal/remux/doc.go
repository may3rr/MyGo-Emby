// Package remux turns media files that AVPlayer cannot open (Matroska,
// MPEG-TS, MP4 with hev1/avc3 tags, or a non-default audio track) into
// fragmented-MP4 HLS served on 127.0.0.1. Video always stays H.264/HEVC and
// is copied, so HDR/Dolby Vision metadata is not re-encoded. AAC, AC-3 and
// E-AC-3 are copied by the default pure-Go path.
//
// When the selected audio codec cannot be written to fMP4 (DTS, TrueHD,
// Opus, FLAC and similar), Source.AudioTranscode may use an optional local
// FFmpeg as an audio-only exception: it decodes one track into AAC-LC
// 48 kHz stereo and pipes each requested segment through memory. No movie or
// temporary media file is written to disk, and video is never sent through
// FFmpeg. The server finds MYGO_FFMPEG, /opt/homebrew/bin/ffmpeg,
// /usr/local/bin/ffmpeg, then PATH; a GUI app therefore does not depend on
// the launching shell's PATH containing Homebrew.
//
// # How it works
//
// A Session reads the remote file's index once when it opens (Matroska
// Cues, the MP4 sample table; MPEG-TS has none) and lays out a VOD
// playlist whose length is known from the start, so AVPlayer can seek
// anywhere. A segment is produced only when AVPlayer asks for it: the
// session reads just that segment's bytes with an HTTP range request,
// repackages them as moof+mdat fragments and streams them back while it
// reads. Seeking is AVPlayer asking for another segment. The segment after
// the one asked for is read ahead once, within a memory limit, to hide the
// object storage's latency. When AVPlayer drops a request, its upstream read
// stops.
//
// # Use
//
//	srv, err := remux.NewServer()          // once per app; srv.Close() at exit
//	...
//	switch d := remux.Decide(&playback.Source, audioIndex); d.Mode {
//	case remux.DirectPlay:
//		url = playback.StreamURL           // AVPlayer opens the file itself
//	case remux.Remux:
//		sess, err := srv.Open(ctx, remux.Source{
//			URL:         playback.StreamURL, // static stream, token in the query
//			MediaSource: &playback.Source,
//			AudioIndex:  d.AudioIndex,
//			AudioTranscode: d.AudioTranscode,
//			Duration:    playback.Runtime(),
//			Start:       resume,
//			OnCues: func(track int, cues []emby.Cue) {
//				feeds[track].Add(cues...)  // any goroutine; must not block
//			},
//		})
//		url = sess.PlaylistURL             // video.OpenURL(url)
//		// sess.Close() when the player closes or the stream is replaced.
//	case remux.Unsupported:
//		// show d.Reason
//	}
//
// Switching the audio track opens a new Session with the other AudioIndex
// (and closes the old one); the player then seeks to where it was.
//
// # Subtitles
//
// Embedded text subtitles (SubRip, ASS/SSA as plain text, WebVTT, MP4
// mov_text) of every track are extracted from the bytes segments read
// anyway, and delivered through Source.OnCues keyed by the Emby
// MediaStream index of their track (Track.Index), so the player can switch
// subtitle tracks without reopening. AVPlayer loads segments ahead of the
// playhead, so cues arrive before they show. A cue that began before a
// segment and still shows in it is delivered with that segment too (from
// the MP4 sample table, or the Matroska Cues of subtitle blocks), so after
// a seek the line on screen is there without any extra call. Cues may come
// more than once; the session drops repeats it already delivered. Graphic
// subtitles (PGS, VobSub, DVB) are not extracted: Track.Supported is false.
//
// # Limits
//
// MPEG-TS and Matroska files without Cues have no index: segment times in
// the playlist are a grid and the session finds positions by bisection, so
// seeking costs a few extra round trips and EXTINF may differ from a
// segment's real length by up to one GOP. HDR plays as AVPlayer delivers
// it; the macOS native video layer preserves HDR/EDR and Dolby Vision, while
// other platforms remain limited by their video output. Dolby Vision profile
// 5 is passed as dvh1; other profiles play their HEVC base layer.
//
// The FFmpeg audio exception needs a build with the native AAC encoder; it
// has been verified with FFmpeg 8.0.1. If it is absent, a source that needs it
// fails clearly rather than dropping the audio track. Audio-only transcoding
// is performed per segment and cancelled with the session or HTTP read; no
// subprocess outlives a closed player.
//
// Container PTS are normalized to the media origin before planning HLS
// segments. FFmpeg's input seek is relative to that origin, while the AAC
// samples are mapped back to the same content-relative timeline. This keeps
// Matroska files whose first packet starts far from zero in sync, including
// after seeking.
//
// AVPlayer must be allowed to load http://127.0.0.1 (App Transport
// Security); see the project's remux report for the Info.plist key.
package remux
