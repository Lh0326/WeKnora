package learning

// LevelResult carries the derived display grade plus the honesty flag: a
// state with fewer than LowConfidenceEvidence folded events is marked
// low-confidence so the UI can render it as provisional rather than as a
// verdict.
type LevelResult struct {
	Level         Level
	LowConfidence bool
}

// levelRank maps tiers to ordinals so promotion/demotion can walk tiers.
func levelRank(l Level) int {
	switch l {
	case LevelTouched:
		return 1
	case LevelFamiliar:
		return 2
	case LevelMastered:
		return 3
	default:
		return 0
	}
}

func levelOfRank(r int) Level {
	switch r {
	case 1:
		return LevelTouched
	case 2:
		return LevelFamiliar
	case 3:
		return LevelMastered
	default:
		return LevelUnseen
	}
}

// upThreshold is the promotion gate into rank r (r >= 1).
func upThreshold(r int) float64 {
	switch r {
	case 1:
		return LevelTouchedUp
	case 2:
		return LevelFamiliarUp
	default:
		return LevelMasteredUp
	}
}

// downThreshold is the demotion gate out of rank r (r >= 1).
func downThreshold(r int) float64 {
	switch r {
	case 1:
		return LevelTouchedDown
	case 2:
		return LevelFamiliarDown
	default:
		return LevelMasteredDown
	}
}

// LevelOf maps a decayed probability to the four-tier grade with hysteresis.
// Promotion compares against the Up threshold of the next tier, demotion
// against the Down threshold of the current one, so p_eff jitter inside a
// band (e.g. 0.22..0.30 around "touched") never flaps the level — holding
// the previous level is exactly what the band is for. Both directions walk
// tier by tier, so a single call settles at the highest tier whose gates are
// cleared.
func LevelOf(pEff float64, evidenceCount int, prevLevel Level) LevelResult {
	r := levelRank(prevLevel)

	for next := r + 1; next <= 3 && pEff >= upThreshold(next); next++ {
		r = next
	}
	for r > 0 && pEff < downThreshold(r) {
		r--
	}

	return LevelResult{
		Level:         levelOfRank(r),
		LowConfidence: evidenceCount < LowConfidenceEvidence,
	}
}
