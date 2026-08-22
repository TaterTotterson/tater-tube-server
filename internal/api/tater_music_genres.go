package api

import (
	"strings"
	"unicode"
)

type taterMusicGenreFamily struct {
	Name    string
	Markers []string
}

// These are broad browse/search families, not a replacement for a track's
// specific genre. A track tagged "Roots Reggae", for example, keeps that tag
// and also becomes discoverable as "Reggae".
var taterMusicGenreFamilies = []taterMusicGenreFamily{
	{Name: "Alternative", Markers: []string{"alternative", "indie"}},
	{Name: "Blues", Markers: []string{"blues"}},
	{Name: "Children's", Markers: []string{"children s music", "kids music", "nursery rhyme"}},
	{Name: "Classical", Markers: []string{"classical", "baroque", "opera", "orchestral", "chamber music"}},
	{Name: "Country", Markers: []string{"country", "bluegrass", "americana", "honky tonk"}},
	{Name: "Dance", Markers: []string{"dance", "disco"}},
	{Name: "Electronic", Markers: []string{"electronic", "electronica", "edm", "house", "techno", "trance", "ambient", "dubstep", "drum and bass", "dnb"}},
	{Name: "Folk", Markers: []string{"folk", "singer songwriter"}},
	{Name: "Gospel", Markers: []string{"gospel", "worship", "christian music"}},
	{Name: "Hip-Hop/Rap", Markers: []string{"hip hop", "hiphop", "rap", "trap", "boom bap"}},
	{Name: "Holiday", Markers: []string{"holiday", "christmas music"}},
	{Name: "Jazz", Markers: []string{"jazz", "bebop", "swing"}},
	{Name: "Latin", Markers: []string{"latin", "salsa", "reggaeton", "bachata", "merengue", "bossa nova"}},
	{Name: "Metal", Markers: []string{"metal"}},
	{Name: "New Age", Markers: []string{"new age"}},
	{Name: "Pop", Markers: []string{"pop", "kpop"}},
	{Name: "Punk", Markers: []string{"punk"}},
	{Name: "R&B/Soul", Markers: []string{"r b", "rnb", "rhythm and blues", "soul", "funk", "motown"}},
	{Name: "Reggae", Markers: []string{"reggae", "dub", "dancehall", "ska", "rocksteady"}},
	{Name: "Rock", Markers: []string{"rock", "grunge"}},
	{Name: "Soundtrack", Markers: []string{"soundtrack", "film score", "video game music"}},
	{Name: "Spoken Word", Markers: []string{"spoken word", "audiobook"}},
	{Name: "World", Markers: []string{"world music", "afrobeat", "highlife"}},
}

var taterMusicCanonicalGenreNames = func() map[string]string {
	result := map[string]string{
		"hip hop":          "Hip-Hop/Rap",
		"hiphop":           "Hip-Hop/Rap",
		"hip hop rap":      "Hip-Hop/Rap",
		"r b":              "R&B/Soul",
		"rnb":              "R&B/Soul",
		"rhythm and blues": "R&B/Soul",
		"children s music": "Children's",
		"kids music":       "Children's",
		"world music":      "World",
	}
	for _, family := range taterMusicGenreFamilies {
		result[normalizeTaterMusicGenreKey(family.Name)] = family.Name
	}
	return result
}()

func normalizeTaterMusicGenreKey(value string) string {
	var builder strings.Builder
	space := false
	for _, char := range strings.ToLower(strings.TrimSpace(value)) {
		if unicode.IsLetter(char) || unicode.IsNumber(char) {
			builder.WriteRune(char)
			space = false
			continue
		}
		if builder.Len() > 0 && !space {
			builder.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(builder.String())
}

func taterMusicGenreMarkerMatches(key, marker string) bool {
	if key == marker {
		return true
	}
	return strings.Contains(" "+key+" ", " "+marker+" ")
}

func expandTaterMusicGenre(value string) []string {
	display := cleanTaterText(value)
	key := normalizeTaterMusicGenreKey(display)
	if display == "" || key == "" {
		return nil
	}
	if canonical := taterMusicCanonicalGenreNames[key]; canonical != "" {
		display = canonical
	}
	result := []string{display}
	for _, family := range taterMusicGenreFamilies {
		for _, marker := range family.Markers {
			if taterMusicGenreMarkerMatches(key, marker) {
				result = append(result, family.Name)
				break
			}
		}
	}
	return result
}

// taterMusicBroadGenres reduces detailed tags to the stable browse families
// used by Tater Tube. It is intentionally conservative so provider styles and
// same-artist inference do not copy album-specific microgenres everywhere.
func taterMusicBroadGenres(values []string) []string {
	result := []string{}
	seen := map[string]bool{}
	for _, value := range values {
		for _, expanded := range expandTaterMusicGenre(value) {
			for _, family := range taterMusicGenreFamilies {
				if !strings.EqualFold(expanded, family.Name) {
					continue
				}
				key := strings.ToLower(family.Name)
				if !seen[key] {
					seen[key] = true
					result = append(result, family.Name)
				}
				break
			}
		}
	}
	return result
}
