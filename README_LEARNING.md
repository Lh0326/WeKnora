# Personal knowledge learning prototype

This optional WeKnora extension connects four functions: source-grounded learning units, personal evidence, budgeted next steps, and a visual constellation. It targets a runnable algorithm prototype; its scores are not calibrated ability probabilities and its offline checks do not establish improved human learning outcomes.

## Behavior

- A learning unit has a goal, applicable conditions, sources, and optional independent checks. Related links support exploration; reviewed prerequisites constrain ordering. Conflicting uses of the same term can remain separate.
- Reading records progress. Self reports change study preferences. Only eligible independent checks contribute performance evidence. Unobserved performance is not displayed as a numeric ability estimate.
- The learner can select a module. The planner first selects one focus goal using due recall, unresolved difficulty, recent study and declared prerequisites, then selects feasible steps within that goal's prerequisite closure. Only necessary prerequisites can cross the selected module. Reading prepares the next reading step without certifying ability; optional checks do not block new reading. The UI does not require a time-budget choice.
- The planner's exactness and utility bounds concern the selected goal's feasible step combinations, not optimal goal selection or measured learning gain. Large searches disclose approximation. Each recorded action triggers a fresh plan.
- The constellation, reader, plan and assistant use the same personal state. Knowledge bases without units retain page-based learning with the same separation between reading and checks.
- Real recall feedback uses FSRS. Reading and repeated self reports do not fabricate recall success.
- Personal profiles are scoped by the authenticated tenant and subject. Viewing, export, collection preferences and deletion are supported; deletion fences stale writes. Export offers a readable HTML history and the original JSON across that person's knowledge bases. Reading, self reports and checks remain separate; historical records are not a current ability certificate or a promise of one-click restoration.

## Enable and run

Use the standard WeKnora setup documented in the main README. Set `LEARNING_ENABLE=true` on the backend. No new service or frontend runtime dependency is required. The only additional Go library is `github.com/open-spaced-repetition/go-fsrs/v4`.

This submission is based on WeKnora 0.7.2 main commit `fc9d6f8c347c7fb15017e0157dfabf565fb68524`. Its learning migrations follow main's existing message-usage and tenant-skill migrations: PostgreSQL 000087–000101; SQLite 000013–000027. Apply them through the standard migrator to a main-based or fresh database. Experimental databases created by older topic branches used different migration numbers and require a separate migration plan; do not point this checkout at such a database without that plan.

Source material must be available in the selected knowledge base. An authorized knowledge-base writer can request candidates using `POST /api/v1/learning/kb/:kb_id/components/draft`. Candidates are not automatically published. Review conditions, explanation, quotations and relationships before importing a ready pack. The optional `cmd/learning-component-import` CLI targets a local development PostgreSQL instance and imports shared definitions only; it does not create learner results. A pack may have no checks and still support reading and recall.

## Validation

For the current production-algorithm verification entry point, see [the reproducible prototype validation guide](docs/evaluation/learning-prototype/README.md). Its single-command runner records source fingerprints, constraint regressions and synthetic planning comparisons; these checks do not establish human learning gains.

```sh
go test ./internal/database ./internal/application/service/learning/... ./internal/application/repository ./internal/handler/... ./internal/router ./internal/types ./cmd/learning-bench ./cmd/learning-component-import
npm --prefix frontend test
npm --prefix frontend run type-check
npm --prefix frontend run build
python -m unittest discover -s scripts -p 'test_learning_assessment.py'
```

Go tests require the normal project CGO toolchain, including SQLite headers. Algorithm fixtures under `internal/application/service/learning/testdata` are synthetic. They check source versioning, scope, evidence separation, feasible sequencing and declared-utility optimization. They are not real learner records or automatically validated semantic labels.

The independent assessment workflow, held-out challenge template and engineering example are documented in [the evaluation guide](docs/evaluation/objective-learning/README.md). It freezes state before presenting a separate challenge, scores outside the learning profile, and retains exclusions and missing outcomes. Report genuine participant results separately from engineering examples.

The compact public response-prediction comparison uses [a fixed protocol](docs/evaluation/response-prediction/evaluation-protocol.json) and [a schema audit](docs/evaluation/response-prediction/schema-audit.md). `scripts/learning_framework_benchmark.py` requires NumPy and local copies of the two public datasets named by that protocol. It splits by learner, selects on validation data and preserves test and transfer limitations. Public-corpus parameters are not deployed as personal component mastery.

## Boundaries

Candidate generation still needs source review. Tests and synthetic planning comparisons demonstrate specific algorithmic properties; they do not prove faster learning, general content accuracy or enterprise calibration. User studies must use genuinely independent outcomes and controlled conditions before making those claims. Personal reports, learner exports, credentials, model-run logs and compiled binaries are excluded from this source submission.

Detailed node definitions, evidence rules, source boundaries and implementation entry points are described in [the design note](docs/learning-design.md).
