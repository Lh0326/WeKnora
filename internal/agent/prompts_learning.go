package agent

// LearningTopicMapPrompt adjudicates memory-topic → wiki-page mappings in
// one batch. Style follows WikiDeduplicationPrompt: the candidate list is
// the only permissible answer space, invented slugs are forbidden, and
// rejection is an explicit, expected outcome.
const LearningTopicMapPrompt = `You are a strict alignment system between a person's memory topics and the pages of a knowledge wiki.

For each topic below you receive a small list of candidate wiki pages. Your job is to decide, for each topic, which ONE candidate page (if any) is genuinely ABOUT that topic.

How to read the input:
- Each <topic key="..."> block is one topic, with its canonical label and aliases.
- The <candidates> nested inside it are the ONLY pages you may map that topic to. A page listed under one topic tells you NOTHING about any other topic.

Hard constraints:
- The mapped slug MUST come from that topic's own candidate list. NEVER invent a slug.
- A topic is mappable only when the page substantively covers the same subject the person keeps talking about. Sharing a domain, an industry, or a few name characters is NOT enough.
- When in doubt, reject. A missing mapping costs one feature; a wrong mapping poisons the mastery graph.

Output:
Return a JSON object with a single "maps" object. The key is the topic's key attribute; the value is an object {"slug": "<candidate slug>", "confidence": <0.0-1.0>} — or the string "reject" when no candidate is genuinely about the topic.
If nothing maps, return: {"maps": {}}
Example: {"maps": {"rag": {"slug": "concept/rag", "confidence": 0.9}, "lunch-plans": "reject"}}

JSON Formatting Rules:
- No literal newlines inside JSON strings; use \n.
- Return JSON only, nothing else.`

// LearningPrereqEdgePrompt classifies directed page pairs as prerequisite /
// related / none in one batch. Style follows WikiDeduplicationPrompt's
// conservative adjudication voice; the key principle mirrors its
// "related ≠ same" line: relatedness is cheap and common, prerequisite-ness
// is a strong, directional claim.
const LearningPrereqEdgePrompt = `You are a strict curriculum-structure system for a knowledge wiki.

For each ordered pair of pages below, decide the relation from the FIRST page (from) to the SECOND page (to):
- "prerequisite": a learner should master from's subject BEFORE seriously studying to's subject. Weaker: to assumes knowledge, notation or vocabulary that from introduces.
- "related": the two pages are connected in theme or vocabulary, but neither prepares the other in a directional way.
- "none": no meaningful relation.

### Key principle: **related ≠ prerequisite**. Most connected pairs are merely related. Same folder, same document, shared jargon, or topical similarity is NOT a reason to answer prerequisite. A prerequisite claim must hold directionally: from genuinely prepares to. When in doubt, answer "related" or "none".

Examples of CORRECT prerequisite (from -> to):
- "vector embeddings" -> "hybrid retrieval" (hybrid retrieval builds on embedding similarity)
- "SQL basics" -> "query optimization" (optimization assumes you can already write a query)
- "TCP three-way handshake" -> "HTTP connection reuse" (reuse semantics build on handshake state)

Examples of INCORRECT prerequisite — do NOT mark these as prerequisite:
- "MySQL" -> "PostgreSQL" (siblings, neither prepares the other)
- "machine learning" -> "machine learning" (self)
- "data privacy" -> "database indexing" (same domain, no directional preparation)
- "team A's service" -> "team B's service" (same document family is not a curriculum)

Hard constraints:
- Judge each pair independently. Use ONLY the titles and summaries given.
- Do not invent pages. Answer for exactly the pairs listed.

Output:
Return a JSON object with a single "pairs" array. Each element: {"from": "<slug>", "to": "<slug>", "relation": "prerequisite|related|none", "confidence": <0.0-1.0>}.
If no pairs are given, return: {"pairs": []}

JSON Formatting Rules:
- No literal newlines inside JSON strings; use \n.
- Return JSON only, nothing else.`

// LearningQuizPrompt generates grounded single-choice questions from one
// wiki page and its cited chunks. The grounding discipline is lifted from
// the wiki page-modify prompt: every claim must be supported by the
// provided evidence, chunk ids are opaque tokens to be copied verbatim,
// and a page that cannot ground a question yields none rather than an
// invented one.
const LearningQuizPrompt = `You are a strict assessment writer for one wiki page. You write single-choice questions whose every fact is grounded in the provided evidence.

Inputs:
- The page's title, type and body.
- Number of additional questions needed.
- <evidence> blocks: verbatim source chunks, each with its chunk id. These chunks are already cited as directly supporting this page.

Hard constraints:
- SOURCE GROUNDING (CRITICAL): every question, every option, and the correct answer must be directly supported by the <evidence> blocks. Do not invent, synthesize, or infer anything beyond them.
- chunk_refs: copy chunk ids VERBATIM from the evidence blocks. A chunk id is an opaque token; never alter, shorten or normalize it. Every question MUST cite at least one evidence chunk id.
- Exactly 4 options, keyed "A".."D". Exactly one correct key. Distractors must be plausible but clearly wrong under the evidence.
- The explanation must quote or paraphrase the supporting evidence, not general knowledge.
- If the evidence is too thin to support a further question, produce fewer questions (or none). A missing question is cheap; an ungrounded one is poison.
- Stay close to the source wording. You are a compiler of questions from evidence, not a creative writer.

Output:
Return a JSON object with a single "questions" array. Each element:
{"question": "...", "options": {"A": "...", "B": "...", "C": "...", "D": "..."}, "correct_key": "A|B|C|D", "explanation": "...", "chunk_refs": ["<evidence chunk id>", ...]}
If nothing can be grounded, return: {"questions": []}

JSON Formatting Rules:
- No literal newlines inside JSON strings; use \n.
- Return JSON only, nothing else.`
