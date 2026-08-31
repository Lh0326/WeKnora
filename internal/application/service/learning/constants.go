package learning

import "time"

// Fold weights and thresholds for the mastery layer. Every value here is an
// explicit, defensible engineering choice rather than a fitted parameter:
// natural-interaction signals carry no ground truth, so fitting would be
// false precision. Each value is tunable offline — learning-bench replay
// (stage 5) is the designated tuning loop (see the -w override flag), and
// weights are frozen on the event rows at append time so retuning never
// rewrites visible history. Declared as vars (not consts) precisely so the
// bench tool can sweep them; production code never assigns them.
var (
	// WeightAnswerCite: the answer cited a chunk/document this node covers.
	// The baseline positive signal.
	WeightAnswerCite = 1.0
	// WeightCrossRef: the node was re-touched from a different question
	// outside the re-ask window — a deliberate return.
	WeightCrossRef = 1.6
	// WeightReAsk: the same node was asked about again inside the re-ask
	// window, reading as "the previous answer did not land".
	WeightReAsk = -0.8
	// WeightTopicSignal: channel-B topic projection. Indirect, discounted.
	WeightTopicSignal = 0.5
	// WeightQuizCorrect: the only direct mastery evidence, so it outweighs
	// every indirect signal.
	WeightQuizCorrect = 2.2
	// WeightQuizWrong: the only direct negative mastery evidence.
	WeightQuizWrong = -1.5
	// QuizRepeatDecay: the n-th rapid attempt on the same item earns
	// Decay^(n-1) of the base weight, so re-answering within the re-ask
	// window cannot farm score. The exponent is capped by
	// QuizRepeatDecayCap: practice must always move the needle (weight
	// never below Decay^Cap of base), and the logit clamp bounds the
	// absolute total anyway.
	QuizRepeatDecay    = 0.5
	QuizRepeatDecayCap = 2
	// BackfillDiscount: replayed doc-affinity history is document-grained
	// and therefore coarser than live chunk-grained citations.
	BackfillDiscount = 0.5

	// LogitFloor/LogitCap clamp the accumulated logit so no amount of
	// farming can push a probability to certainty.
	LogitFloor = -4.0
	LogitCap   = 4.0

	// StabilityBaseDays: initial stability in days. StabilityGrowth and
	// StabilitySaturationExp shape the FSRS-inspired growth
	// (s = Base·(1+Growth·pos)^SatExp·LapseShrink^neg): the exponent < 1
	// makes each repetition add less than the last, and each negative
	// evidence multiplies by the lapse shrink so a lapse weakens the
	// memory without zeroing it. With the power-law decay curve, the
	// value reads as "days until retrievability falls to 90%".
	StabilityBaseDays      = 14.0
	StabilityGrowth        = 0.35
	StabilitySaturationExp = 0.5
	StabilityLapseShrink   = 0.85

	// Level thresholds with hysteresis: promotion uses the Up value,
	// demotion the Down value, so p_eff jitter inside a band never flaps
	// the displayed level.
	LevelTouchedUp    = 0.30
	LevelTouchedDown  = 0.22
	LevelFamiliarUp   = 0.55
	LevelFamiliarDown = 0.45
	LevelMasteredUp   = 0.80
	LevelMasteredDown = 0.70

	// LowConfidenceEvidence: fewer folded events than this marks the state
	// low-confidence rather than trusting the number.
	LowConfidenceEvidence = 3

	// RecommendEpsilon: probability of the exploration slot that randomly
	// surfaces an unseen node, keeping the recommender out of a filter
	// bubble. Tunable via learning-bench replay.
	RecommendEpsilon = 0.15

	// EdgeConfidenceMin: prerequisite edges below this adjudication
	// confidence are dropped rather than stored.
	EdgeConfidenceMin = 0.7
	// MapConfidenceMin: topic→slug mappings below this confidence are not
	// persisted.
	MapConfidenceMin = 0.7

	// QuizItemsPerSlug: cap of active questions maintained per node.
	QuizItemsPerSlug = 3

	// PrereqBlockedFloor: a prerequisite whose p_eff sits below this with
	// real evidence counts as blocking, triggering the neighbour bypass.
	PrereqBlockedFloor = 0.30
)

// ReAskWindowHours stays a typed constant: it multiplies time.Hour, which
// untyped vars cannot do — and the sweep has no reason to touch it.
const ReAskWindowHours = 48

// ReadRapidDedup is the refresh shield on the page-read signal: a second
// open of the same page within it records nothing (it is an F5 or a
// double-click, not studying). Opens beyond it always land in the timeline;
// only the first per 48h carries mastery weight.
const ReadRapidDedup = time.Minute
