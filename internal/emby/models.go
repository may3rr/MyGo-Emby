package emby

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
)

// Item types (BaseItemDto.Type).
const (
	TypeMovie            = "Movie"
	TypeSeries           = "Series"
	TypeSeason           = "Season"
	TypeEpisode          = "Episode"
	TypeBoxSet           = "BoxSet"
	TypePerson           = "Person"
	TypeFolder           = "Folder"
	TypeCollectionFolder = "CollectionFolder"
	TypeUserView         = "UserView"
	TypeMusicVideo       = "MusicVideo"
	TypeVideo            = "Video"
	TypeTrailer          = "Trailer"
	TypePlaylist         = "Playlist"
	TypeMusicAlbum       = "MusicAlbum"
	TypeMusicArtist      = "MusicArtist"
	TypeAudio            = "Audio"
	TypeChannel          = "Channel"
	TypeTvChannel        = "TvChannel"
)

// Collection types of library views (BaseItemDto.CollectionType).
const (
	CollectionMovies      = "movies"
	CollectionTVShows     = "tvshows"
	CollectionMusic       = "music"
	CollectionMusicVideos = "musicvideos"
	CollectionHomeVideos  = "homevideos"
	CollectionBoxSets     = "boxsets"
	CollectionPlaylists   = "playlists"
	CollectionFolders     = "folders"
	CollectionLiveTV      = "livetv"
	CollectionBooks       = "books"
	CollectionPhotos      = "photos"
	CollectionMixed       = "mixed"
)

// Item is a BaseItemDto: movie, series, season, episode, folder, person,
// library view and so on. Fields absent in the JSON keep their zero value;
// fields where zero is meaningful (episode and season numbers, default
// stream indexes) are pointers.
type Item struct {
	ID             string `json:"Id"`
	ServerID       string `json:"ServerId,omitempty"`
	Name           string `json:"Name"`
	OriginalTitle  string `json:"OriginalTitle,omitempty"`
	SortName       string `json:"SortName,omitempty"`
	Type           string `json:"Type"`
	CollectionType string `json:"CollectionType,omitempty"`
	MediaType      string `json:"MediaType,omitempty"`
	IsFolder       bool   `json:"IsFolder"`
	ParentID       string `json:"ParentId,omitempty"`
	Container      string `json:"Container,omitempty"`
	LocationType   string `json:"LocationType,omitempty"`

	Overview        string            `json:"Overview,omitempty"`
	Taglines        []string          `json:"Taglines,omitempty"`
	Genres          []string          `json:"Genres,omitempty"`
	GenreItems      []NameID          `json:"GenreItems,omitempty"`
	Studios         []NameID          `json:"Studios,omitempty"`
	Tags            []string          `json:"Tags,omitempty"`
	People          []Person          `json:"People,omitempty"`
	ProviderIDs     map[string]string `json:"ProviderIds,omitempty"`
	ExternalURLs    []ExternalURL     `json:"ExternalUrls,omitempty"`
	ProductionYear  int               `json:"ProductionYear,omitempty"`
	PremiereDate    Date              `json:"PremiereDate"`
	EndDate         Date              `json:"EndDate"`
	DateCreated     Date              `json:"DateCreated"`
	Status          string            `json:"Status,omitempty"` // series: "Continuing" or "Ended"
	RunTimeTicks    int64             `json:"RunTimeTicks,omitempty"`
	CommunityRating float64           `json:"CommunityRating,omitempty"`
	CriticRating    float64           `json:"CriticRating,omitempty"`
	OfficialRating  string            `json:"OfficialRating,omitempty"`

	// Images.
	ImageTags                map[string]string `json:"ImageTags,omitempty"`
	BackdropImageTags        []string          `json:"BackdropImageTags,omitempty"`
	PrimaryImageAspectRatio  float64           `json:"PrimaryImageAspectRatio,omitempty"`
	ParentBackdropItemID     string            `json:"ParentBackdropItemId,omitempty"`
	ParentBackdropImageTags  []string          `json:"ParentBackdropImageTags,omitempty"`
	ParentThumbItemID        string            `json:"ParentThumbItemId,omitempty"`
	ParentThumbImageTag      string            `json:"ParentThumbImageTag,omitempty"`
	ParentLogoItemID         string            `json:"ParentLogoItemId,omitempty"`
	ParentLogoImageTag       string            `json:"ParentLogoImageTag,omitempty"`
	ParentPrimaryImageItemID string            `json:"ParentPrimaryImageItemId,omitempty"`
	ParentPrimaryImageTag    string            `json:"ParentPrimaryImageTag,omitempty"`
	SeriesPrimaryImageTag    string            `json:"SeriesPrimaryImageTag,omitempty"`
	SeriesThumbImageTag      string            `json:"SeriesThumbImageTag,omitempty"`
	AlbumID                  string            `json:"AlbumId,omitempty"`
	AlbumPrimaryImageTag     string            `json:"AlbumPrimaryImageTag,omitempty"`

	// TV.
	SeriesID          string `json:"SeriesId,omitempty"`
	SeriesName        string `json:"SeriesName,omitempty"`
	SeasonID          string `json:"SeasonId,omitempty"`
	SeasonName        string `json:"SeasonName,omitempty"`
	IndexNumber       *int   `json:"IndexNumber,omitempty"`
	IndexNumberEnd    *int   `json:"IndexNumberEnd,omitempty"`
	ParentIndexNumber *int   `json:"ParentIndexNumber,omitempty"`
	AirTime           string `json:"AirTime,omitempty"`

	// Folders.
	ChildCount         int `json:"ChildCount,omitempty"`
	RecursiveItemCount int `json:"RecursiveItemCount,omitempty"`

	// Music.
	Album       string   `json:"Album,omitempty"`
	AlbumArtist string   `json:"AlbumArtist,omitempty"`
	Artists     []string `json:"Artists,omitempty"`

	UserData     *UserData                         `json:"UserData,omitempty"`
	MediaSources []MediaSource                     `json:"MediaSources,omitempty"`
	MediaStreams []MediaStream                     `json:"MediaStreams,omitempty"`
	Chapters     []Chapter                         `json:"Chapters,omitempty"`
	Trickplay    map[string]map[string]TrickplayInfo `json:"Trickplay,omitempty"`
}

// NameID is a name with an optional id (studios, genre items).
type NameID struct {
	Name string `json:"Name"`
	ID   string `json:"Id,omitempty"`
}

// ExternalURL is a link to an external site (IMDb, TMDb…).
type ExternalURL struct {
	Name string `json:"Name"`
	URL  string `json:"Url"`
}

// Person is a cast or crew member of an item.
type Person struct {
	ID              string `json:"Id"`
	Name            string `json:"Name"`
	Role            string `json:"Role,omitempty"`
	Type            string `json:"Type,omitempty"` // Actor, Director, Writer, Producer, GuestStar…
	PrimaryImageTag string `json:"PrimaryImageTag,omitempty"`
}

// Chapter is a chapter marker.
type Chapter struct {
	StartPositionTicks int64  `json:"StartPositionTicks"`
	Name               string `json:"Name"`
	ImageTag           string `json:"ImageTag,omitempty"`
}

// Start returns the chapter start as a duration.
func (c Chapter) Start() time.Duration { return TicksToDuration(c.StartPositionTicks) }

// UserData is the per-user state of an item.
type UserData struct {
	PlaybackPositionTicks int64   `json:"PlaybackPositionTicks"`
	PlayCount             int     `json:"PlayCount"`
	IsFavorite            bool    `json:"IsFavorite"`
	Played                bool    `json:"Played"`
	UnplayedItemCount     int     `json:"UnplayedItemCount,omitempty"`
	PlayedPercentage      float64 `json:"PlayedPercentage,omitempty"`
	LastPlayedDate        Date    `json:"LastPlayedDate"`
	Key                   string  `json:"Key,omitempty"`
	ItemID                string  `json:"ItemId,omitempty"`
}

// MediaSource is one playable version of an item.
type MediaSource struct {
	ID                         string        `json:"Id"`
	Name                       string        `json:"Name,omitempty"`
	Path                       string        `json:"Path,omitempty"`
	Protocol                   string        `json:"Protocol,omitempty"` // File, Http…
	Container                  string        `json:"Container,omitempty"`
	Size                       int64         `json:"Size,omitempty"`
	Bitrate                    int64         `json:"Bitrate,omitempty"`
	RunTimeTicks               int64         `json:"RunTimeTicks,omitempty"`
	IsRemote                   bool          `json:"IsRemote,omitempty"`
	SupportsDirectPlay         bool          `json:"SupportsDirectPlay"`
	SupportsDirectStream       bool          `json:"SupportsDirectStream"`
	SupportsTranscoding        bool          `json:"SupportsTranscoding"`
	TranscodingURL             string        `json:"TranscodingUrl,omitempty"`
	TranscodingSubProtocol     string        `json:"TranscodingSubProtocol,omitempty"`
	TranscodingContainer       string        `json:"TranscodingContainer,omitempty"`
	DirectStreamURL            string        `json:"DirectStreamUrl,omitempty"`
	ETag                       string        `json:"ETag,omitempty"`
	LiveStreamID               string        `json:"LiveStreamId,omitempty"`
	RequiresOpening            bool          `json:"RequiresOpening,omitempty"`
	DefaultAudioStreamIndex    *int          `json:"DefaultAudioStreamIndex,omitempty"`
	DefaultSubtitleStreamIndex *int          `json:"DefaultSubtitleStreamIndex,omitempty"`
	VideoType                  string        `json:"VideoType,omitempty"`
	MediaStreams               []MediaStream `json:"MediaStreams,omitempty"`
	TranscodeReasons           []string      `json:"TranscodeReasons,omitempty"`
}

// DisplayName returns a label for a version picker.
func (m *MediaSource) DisplayName() string {
	if m.Name != "" {
		return m.Name
	}
	for _, s := range m.MediaStreams {
		if s.Type == StreamVideo && s.DisplayTitle != "" {
			return s.DisplayTitle
		}
	}
	return strings.ToUpper(firstContainer(m.Container))
}

// Streams returns the source's streams of the given type (StreamAudio…).
func (m *MediaSource) Streams(typ string) []MediaStream {
	var out []MediaStream
	for _, s := range m.MediaStreams {
		if s.Type == typ {
			out = append(out, s)
		}
	}
	return out
}

// Stream returns the stream with the given index, or nil.
func (m *MediaSource) Stream(index int) *MediaStream {
	for i := range m.MediaStreams {
		if m.MediaStreams[i].Index == index {
			return &m.MediaStreams[i]
		}
	}
	return nil
}

// VideoStream returns the first video stream, or nil.
func (m *MediaSource) VideoStream() *MediaStream {
	for i := range m.MediaStreams {
		if m.MediaStreams[i].Type == StreamVideo {
			return &m.MediaStreams[i]
		}
	}
	return nil
}

// Media stream types (MediaStream.Type).
const (
	StreamVideo      = "Video"
	StreamAudio      = "Audio"
	StreamSubtitle   = "Subtitle"
	StreamAttachment = "Attachment"
	StreamData       = "Data"
)

// Subtitle delivery methods (MediaStream.DeliveryMethod).
const (
	DeliveryEncode   = "Encode"
	DeliveryEmbed    = "Embed"
	DeliveryExternal = "External"
	DeliveryHLS      = "Hls"
)

// MediaStream is one video, audio, subtitle or attachment stream.
type MediaStream struct {
	Index                  int     `json:"Index"`
	Type                   string  `json:"Type"`
	Codec                  string  `json:"Codec,omitempty"`
	CodecTag               string  `json:"CodecTag,omitempty"`
	Language               string  `json:"Language,omitempty"`
	DisplayTitle           string  `json:"DisplayTitle,omitempty"`
	DisplayLanguage        string  `json:"DisplayLanguage,omitempty"`
	Title                  string  `json:"Title,omitempty"`
	Profile                string  `json:"Profile,omitempty"`
	IsDefault              bool    `json:"IsDefault"`
	IsForced               bool    `json:"IsForced"`
	IsExternal             bool    `json:"IsExternal"`
	IsHearingImpaired      bool    `json:"IsHearingImpaired,omitempty"`
	IsTextSubtitleStream   bool    `json:"IsTextSubtitleStream"`
	SupportsExternalStream bool    `json:"SupportsExternalStream,omitempty"`
	DeliveryMethod         string  `json:"DeliveryMethod,omitempty"`
	DeliveryURL            string  `json:"DeliveryUrl,omitempty"`
	Path                   string  `json:"Path,omitempty"`
	Width                  int     `json:"Width,omitempty"`
	Height                 int     `json:"Height,omitempty"`
	BitDepth               int     `json:"BitDepth,omitempty"`
	BitRate                int64   `json:"BitRate,omitempty"`
	Channels               int     `json:"Channels,omitempty"`
	ChannelLayout          string  `json:"ChannelLayout,omitempty"`
	SampleRate             int     `json:"SampleRate,omitempty"`
	AverageFrameRate       float64 `json:"AverageFrameRate,omitempty"`
	RealFrameRate          float64 `json:"RealFrameRate,omitempty"`
	Level                  float64 `json:"Level,omitempty"`
	PixelFormat            string  `json:"PixelFormat,omitempty"`
	VideoRange             string  `json:"VideoRange,omitempty"`
	VideoRangeType         string  `json:"VideoRangeType,omitempty"`
	AspectRatio            string  `json:"AspectRatio,omitempty"`
	IsInterlaced           bool    `json:"IsInterlaced,omitempty"`
}

// imageSubtitleCodecs are bitmap subtitle formats that can only be burned in.
var imageSubtitleCodecs = map[string]bool{
	"pgs": true, "pgssub": true, "hdmv_pgs_subtitle": true, "sup": true,
	"dvdsub": true, "dvd_subtitle": true, "vobsub": true, "sub": true,
	"dvbsub": true, "dvb_subtitle": true, "dvb_teletext": true, "xsub": true,
}

// textSubtitleCodecs are formats the server can convert to WebVTT.
var textSubtitleCodecs = map[string]bool{
	"srt": true, "subrip": true, "ass": true, "ssa": true, "vtt": true,
	"webvtt": true, "mov_text": true, "tx3g": true, "text": true, "smi": true,
	"sami": true, "ttml": true, "microdvd": true, "mpl2": true, "subviewer": true,
	"jacosub": true, "realtext": true, "stl": true, "eia_608": true,
}

// IsTextSubtitle reports whether a subtitle stream is text based (and can
// be fetched as WebVTT). The codec wins over the server's
// IsTextSubtitleStream flag, which some Emby-compatible servers set for PGS.
func (s *MediaStream) IsTextSubtitle() bool {
	if s.Type != StreamSubtitle {
		return false
	}
	c := strings.ToLower(s.Codec)
	if imageSubtitleCodecs[c] {
		return false
	}
	return s.IsTextSubtitleStream || textSubtitleCodecs[c]
}

// QueryResult is the envelope of list endpoints.
type QueryResult struct {
	Items            []Item `json:"Items"`
	TotalRecordCount int    `json:"TotalRecordCount"`
	StartIndex       int    `json:"StartIndex"`
}

// Date is a time.Time that accepts the date formats Emby and Jellyfin emit
// ("2018-09-01T00:00:00.0000000Z", with or without zone, date only) and
// null or "" as the zero time. It marshals back to RFC 3339.
type Date struct{ time.Time }

var dateLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05.9999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// UnmarshalJSON implements json.Unmarshaler.
func (d *Date) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	if len(b) == 0 || bytes.Equal(b, []byte("null")) {
		d.Time = time.Time{}
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		d.Time = time.Time{}
		return nil // tolerate unexpected shapes
	}
	d.Time = parseDate(s)
	return nil
}

// MarshalJSON implements json.Marshaler.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(d.UTC().Format(time.RFC3339Nano))
}

func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, l := range dateLayouts {
		if t, err := time.Parse(l, s); err == nil {
			if t.Year() <= 1 {
				return time.Time{} // .NET DateTime.MinValue
			}
			return t
		}
	}
	return time.Time{}
}
