package learning

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// Topic-mapping batch shape: at most this many topics per adjudication
// call, each carrying at most topicMaxCandidates page candidates, so one
// prompt cannot balloon regardless of library size.
const (
	topicBatchSize     = 10
	topicMaxCandidates = 8
)

// topicSignalWindow bounds how recently a topic_signal event may have been
// emitted for a (scope, slug) before another one is allowed — the code
// reading of the design's "每话题-slug 对每晋升周期至多一条". Multiple topics
// mapping onto one node collapse to one signal per window, which is the
// conservative direction.
const topicSignalWindow = 7 * 24 * time.Hour

// pageIdentityKeys returns the normalized strings a page answers to: its
// title, its aliases, and its slug with the type prefix stripped. All are
// normalized through the memory subsystem's topic normaliser so both sides
// of the match speak the same key language.
func pageIdentityKeys(p *types.WikiPage) []string {
	var keys []string
	add := func(s string) {
		if k := types.NormalizeTopicKey(s); k != "" {
			keys = append(keys, k)
		}
	}
	add(p.Title)
	for _, a := range p.Aliases {
		add(a)
	}
	slug := p.Slug
	if i := strings.IndexByte(slug, '/'); i > 0 {
		slug = slug[i+1:]
	}
	add(slug)
	return keys
}

// topicIdentityKeys normalizes a topic's label and aliases.
func topicIdentityKeys(stat *types.MemoryTopicStat) []string {
	var keys []string
	add := func(s string) {
		if k := types.NormalizeTopicKey(s); k != "" {
			keys = append(keys, k)
		}
	}
	add(stat.Topic)
	for _, a := range stat.Aliases {
		add(a)
	}
	return keys
}

// topicCandidates returns the pages a topic may map onto: any page whose
// identity keys equal or prefix-contain one of the topic's keys. The
// candidate list is the adjudicator's entire answer space, so recall here
// is deliberately generous while precision is the LLM's job.
func topicCandidates(stat *types.MemoryTopicStat, pages []*types.WikiPage, maxCandidates int) []string {
	topicKeys := topicIdentityKeys(stat)
	if len(topicKeys) == 0 {
		return nil
	}
	sorted := append([]*types.WikiPage{}, pages...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Slug < sorted[j].Slug })

	var out []string
	for _, p := range sorted {
		if p == nil || p.Slug == "" {
			continue
		}
		matched := false
		for _, pageKey := range pageIdentityKeys(p) {
			if matched {
				break
			}
			for _, topicKey := range topicKeys {
				if pageKey == topicKey || strings.HasPrefix(pageKey, topicKey) || strings.HasPrefix(topicKey, pageKey) {
					matched = true
					break
				}
			}
		}
		if matched {
			out = append(out, p.Slug)
			if len(out) >= maxCandidates {
				break
			}
		}
	}
	return out
}

// topicMapVerdict is one adjudication result.
type topicMapVerdict struct {
	Slug       string  `json:"slug"`
	Confidence float64 `json:"confidence"`
}

// topicMapResponse is the LLM response envelope.
type topicMapResponse struct {
	Maps map[string]interface{} `json:"maps"`
}

// parseTopicVerdict decodes the heterogeneous "value is object or reject"
// map entry. Anything that is neither a usable object nor "reject" is
// treated as reject — the conservative direction.
func parseTopicVerdict(value interface{}) (topicMapVerdict, bool) {
	switch v := value.(type) {
	case map[string]interface{}:
		slug, _ := v["slug"].(string)
		conf, _ := v["confidence"].(float64)
		return topicMapVerdict{Slug: slug, Confidence: conf}, true
	default:
		return topicMapVerdict{}, false
	}
}

// acceptedTopicMaps filters adjudication results down to the mappings that
// may be stored: the slug must come from that topic's own candidate list
// (the deterministic anti-hallucination guard, dedupMergeRejectReason
// style) and the confidence must clear the floor.
func acceptedTopicMaps(
	verdicts topicMapResponse, candidatesByTopic map[string][]string,
) map[string]topicMapVerdict {
	accepted := map[string]topicMapVerdict{}
	for topicKey, value := range verdicts.Maps {
		verdict, ok := parseTopicVerdict(value)
		if !ok {
			continue
		}
		if verdict.Confidence < MapConfidenceMin {
			continue
		}
		allowed := false
		for _, slug := range candidatesByTopic[topicKey] {
			if slug == verdict.Slug {
				allowed = true
				break
			}
		}
		if !allowed {
			continue
		}
		accepted[topicKey] = verdict
	}
	return accepted
}

// topicMapUserPrompt renders the adjudication input in the nested-candidate
// style the wiki deduplication prompt established.
func topicMapUserPrompt(stats []*types.MemoryTopicStat, candidatesByTopic map[string][]string, pagesBySlug map[string]*types.WikiPage) string {
	var b strings.Builder
	b.WriteString("<topics>\n")
	for _, stat := range stats {
		b.WriteString("  <topic key=\"" + stat.NormalizedKey + "\">\n")
		b.WriteString("    <label>" + stat.Topic + "</label>\n")
		for _, a := range stat.Aliases {
			b.WriteString("    <alias>" + a + "</alias>\n")
		}
		b.WriteString("    <candidates>\n")
		for _, slug := range candidatesByTopic[stat.NormalizedKey] {
			if p, ok := pagesBySlug[slug]; ok {
				b.WriteString("      <page slug=\"" + slug + "\" type=\"" + p.PageType + "\">\n")
				b.WriteString("        <name>" + p.Title + "</name>\n")
				b.WriteString("      </page>\n")
			}
		}
		b.WriteString("    </candidates>\n")
		b.WriteString("  </topic>\n")
	}
	b.WriteString("</topics>\n")
	return b.String()
}

// topicMapSchema is the response format for the adjudication call.
var topicMapSchema = jsonRaw(`{
  "type": "object",
  "properties": {
    "maps": {"type": "object"}
  },
  "required": ["maps"]
}`)

// jsonRawMessage is encoding/json.RawMessage under its real name; the
// alias-free form keeps schema literals one word long.
func jsonRaw(s string) json.RawMessage { return json.RawMessage(s) }
