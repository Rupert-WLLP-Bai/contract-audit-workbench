# File metadata policy

This policy defines per-file purpose, authorship, project maintenance, dates, revision, and handling rules. The canonical manifest records the fixed-baseline path set and content-derived purposes. #124/#125 establish header and coverage policy; use the CLI capabilities and maintenance procedure below when preparing or checking changes.

## Current inventory and compatibility fields

The first strict initialization snapshot (#164) contains 1,429 manifest records and 1,261 physical source headers; subsequent tracked additions update those totals. These totals describe different things: the manifest also records files that must remain manifest-only, so their difference is not a count of missing headers. The authoritative header result comes from inspecting the candidate files and running the physical-mirror checks in the trusted BASE-bound gate. Initial bootstrap requires the fixed BASE on the candidate first-parent chain and an exact metadata-only change; if main advances, replay the reviewed net changes from the new main baseline. Treat `header_state: "pending-header-application"` as a retained compatibility label in manifest records; it does not by itself prove that a physical header is absent or required. Resolve status from file bytes, the policy handling mode, and the gate result.

The manifest and its policy/schema remain the inventory and classification sources. The gate runs the five verifier modules from the actual target BASE Git blobs, including when candidate tooling changes. The schema, policy top-level fields and existing classes stay fixed; a new manifest-only class may name only new, previously unclassified exact paths that exist in the candidate and its manifest. Classification rules are not the file inventory: removing or renaming a file must remove its stale manifest entry and preserve verified identity evidence as applicable. The CLI is primarily read-only: `manifest_tool.py plan|report` validates or reports a fixed subject, and `gate_adapter.py check` checks an explicit BASE and candidate SHA. `plan-apply` produces an exact-path insertion plan for an absent header or an already exact mirror; it refuses to replace a different existing mirror. `apply` writes only when given the reviewed plan hash and explicit `--apply`. These commands do not automatically write or regenerate the manifest inventory. Update manifest records through a separately reviewed inventory change, then rerun the fixed-SHA checks.

## Canonical record

The only machine-readable inventory is `scripts/file_metadata/manifest.json`, validated by `scripts/file_metadata/schema.json` and governed by `scripts/file_metadata/policy.json`. It captures every path tracked at its recorded Git baseline plus explicitly listed metadata-policy additions; its inventory object also records excluded concurrent untracked paths. Every tracked file, including manifest-only files, has exactly one record keyed by its repository-relative POSIX path. Do not keep a second inventory in documentation or CI configuration.

Each record contains:

- A file-specific `purpose` describing its responsibility, important input/output, and applicable boundary. A path or filename restatement is not a purpose.
- `authors`, identifying original authorship. For project-authored files use `Bai Junhao`, `baijunhao@cq.chinamobile.com`, contact `+86 13617696321`. For third-party material preserve the original author attribution and source wording; do not replace it with the project author.
- `maintainers`, identifying the project maintenance contact separately from authorship. Use Bai Junhao and the supplied email/phone for project-maintained files, including third-party-derived files.
- `copyright`, recording the actual holder/notice independently from author. For project terms, point to `See LICENSE`; this is a reference to the repository's current license text, not a declaration of SPDX identifier or OSI approval. Preserve third-party copyright and license notices verbatim and record their source location.
- `created` and `updated` date evidence, plus `revision` (`r1`, `r2`, …).
- A `handling` mode; any mode that omits a normal metadata comment requires an `exception` with a rule identifier and a reason specific to that path.

No metadata policy changes the terms in `LICENSE`, grants commercial rights, or transfers copyright.

## Dates and revision

Dates are ISO 8601 calendar dates (`YYYY-MM-DD`) in UTC. They describe Git history evidence, not a claim about when a person first created the content.

- `created` is the earliest traceable Git introduction of the file's content. Follow Git rename history and cite the full commit SHA and date source. When a traced introduction commit uses an earlier path, record that exact path as `source.path`; state when the link comes from Git similarity detection and treat it as lineage evidence, not proof of authorship or actual creation date. Use an explicit `unknown` value with a reason when a shallow/squashed history, imported archive, or ambiguous rename does not support a defensible date. Never substitute the date when metadata is added.
- `updated` is the latest traceable change to file content outside the defined metadata block. Ignore commits that only add or edit metadata. Cite the full commit SHA and date source. If the history cannot identify a content change date, use `unknown` and explain why.
- On rename, carry `created` forward when Git history establishes continuity; preserve `updated` and `revision` if content did not otherwise change. A copy or split is a new path: record the source path and date evidence, then begin its revision at `r1`.
- `r1` is the file's metadata-policy baseline. Increment to the next integer revision when content outside metadata changes, including content changes to binary or manifest-only files. Metadata-only edits do not change `updated` or `revision`. Do not infer revision from product version, API version, database schema, package version, or commit count.
- At the initial inventory, `updated` records the last content change found in Git history, even if it predates the metadata baseline. Adding the metadata block itself does not make the file newly updated.

The manifest records date source type, full commit SHA where available, and any limitation. A checker must parse dates as real UTC calendar dates (JSON Schema `format` support is not uniformly enforced), compare content with the metadata block excluded, and ignore metadata-only edits when computing content changes.

## Source header syntax and placement

For comment-capable text files, add a compact metadata block using that language's native comment syntax. Its first content line is `File metadata (manifest mirror):`; its final content line is `End file metadata.` Between them, use exactly these labels in order: `Purpose`, `Author`, `Contact`, `Created`, `Updated`, `Revision`, `Copyright`. The machine manifest remains authoritative; the header is a readable mirror checked for consistency by #125. Keep purpose specific to the file. Use `Copyright: See LICENSE` for project terms; do not add an SPDX line unless a separate licensing decision supplies one. For third-party files, prefer manifest-only when adding a project header would blur original authorship; if a header is necessary, preserve the upstream author attribution and state project contact as maintainer, not author.

Place the block where it preserves parser and tool requirements:

- Go: after any `//go:build` / `// +build` lines and their required blank line, before the package clause. Preserve generated-code markers and existing copyright blocks exactly.
- Python and shell: after a shebang and any encoding cookie; retain the shebang as line 1.
- TypeScript/TSX: after any required shebang or triple-slash directives and before imports. For JSX, use ordinary line comments before imports, not JSX comments.
- CSS: use a CSS block comment before rules. HTML and Markdown: use an HTML comment before content. YAML/TOML and Makefile/Dockerfile: use only their native `#` comments where a leading comment is accepted.
- For a format not covered here, require a reviewed native syntax and placement rule before writing a header. Never put comment syntax into data that is parsed as content.

## File handling matrix

| Tracked file kind | Handling |
| --- | --- |
| Go, Python, TypeScript/TSX, shell, CSS, HTML, Markdown | Native comment header plus one manifest record, subject to required directive placement and path-specific exceptions. |
| YAML/YML, TOML, Makefile, Dockerfile, `.env.example`, `.conf` | Native `#` comment header only where the consumer accepts a leading comment; otherwise manifest-only with the specific parser/tool constraint recorded. |
| Strict JSON, lockfiles, module checksums, and data formats without safe comments | Manifest-only. JSON stays valid JSON; never use JSONC or inject fields into a third-party/public contract to carry repository metadata. |
| Applied or integrity-checked SQL migrations | Manifest-only. Do not edit historical migration bytes to add metadata; cite the migration path and immutability/integrity reason. A future migration follows its own reviewed authoring convention before application. |
| Frozen regression fixtures under `testdata/` and `web/e2e/fixtures/` | Manifest-only. Preserve fixture bytes and record what behavior/input the fixture represents; do not rewrite a golden fixture just to add metadata. |
| Binary documents, images, fonts, and other assets | Manifest-only. Do not alter payload bytes or embedded metadata; record source, purpose, and rights evidence. |
| Generated output | Manifest-only unless the owning generator is explicitly changed to emit a stable metadata block and its output verification is updated in the same owned change. Record generator/input paths and reason. |
| Third-party-derived or third-party-noticed source | Preserve every upstream attribution, copyright, and license notice byte-for-byte. Use manifest-only or place the project metadata block outside the preserved notice; record source and project maintainer separately. |
| Other or unknown formats | Do not guess. Add a reviewed format rule to `policy.json` before inventorying it. |

`policy.json` names current concrete exceptions and class rules. A future manifest record still names each exact path and gives a path-specific reason; a glob rule alone is not a substitute for that record. An exception only changes where metadata is stored, not the requirement to describe the file or capture its authorship, maintenance, date, revision, and rights evidence.

## Maintenance procedure

1. Fix the trusted BASE and candidate SHA. Enumerate the candidate tracked paths; do not include ignored files or local build output.
2. For each path, inspect content, rename-aware history, parser directives, generator ownership, fixture/frozen status, and existing notices before changing its record or header.
3. Select the narrowest handling rule. Preserve attribution and protected bytes. Record each exception against its exact path, policy rule, and reason.
4. Compare physical headers with their manifest mirrors under the trusted BASE-bound gate. The compatibility value `pending-header-application` is not the verdict. Use the policy handling mode and physical-mirror check to determine whether a header is expected, present, and consistent.
5. For an absent header, create an exact-path `plan-apply` plan against fixed BASE and candidate SHAs; review the paths, original bytes/mode and plan hash, then use `apply --apply` explicitly. A changed existing mirror requires a separately reviewed refresh plan that preserves all body bytes and Git modes and binds the original/output hashes to the fixed subject. These operations do not author or regenerate inventory records. Prepare manifest updates separately, retaining unchanged BASE evidence, incrementing revisions only for body/mode changes, and recording committed source evidence before final validation.
6. Run `gate_adapter.py check` with the actual trusted BASE SHA and fixed candidate SHA, then report the status and evidence. The check validates the candidate registry, physical header mirrors, and tracked-path coverage as implemented. Preserve its exact result; do not infer PASS from record totals or a dry-run plan.
