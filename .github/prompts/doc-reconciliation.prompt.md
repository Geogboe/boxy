---
agent: agent
---
Scan through all markdown files in this repository and attempt to reconcile, validate and ensure consistency.

DO NOT make any changes to code files, only to markdown documentation files.
DO NOT make any changes to documents until you have a approval.
DO NOT touch coding agent files: CLAUDE.md, GEMINI.md, AGENTS.md, .github/* etc.

Summarize in SMALL BATCHES all the proposed changes and ask for approval before making any changes.

Reconciliation Guidelines:

- Docs should not contradict each other and should align with the code snippets in the codebase.
- Docs should have limited code snippets and should instead refer to code in the codebase where possible.
- Docs should not duplicate information that is already captured in code comments or README files in the codebase and should instead refer to those files or sections of those files
- Docs should be well organized in directories that make sense.
- Docs that are no longer applicable or relevant should be marked for deletion.
- CAUTION: Agent coding files: CLAUDE.md, GEMINI.md will point to AGENTS.md so only AGENTS.md should contain the detailed information about agents. Be very careful about modifying these types of files.
- Ensure ALL diagrams are up to date and accurately reflect the current architecture and workflows.
- There's an examples directory. Ensure that any examples mentioned in the docs are present in that directory and are up to date. Try to link to those examples rather than duplicating them in the docs unless the inline example is very small and simple.

Optimizations to consider:

- Inventory all available documentation files and their purposes first without actually reading in all their contents.
- Work in a section by section manner. Rather than reading every possible document, focus on a section of a document, then cross reference with other documents or relevant sections of other documents, or code files. Work iteratively.
- Work in a graph traversal manner. Start from a central document (like an architecture overview) and branch out to related documents, ensuring consistency as you go.
- Prioritize high-level architecture and design documents first, then move to more detailed implementation documents.
- Use your own built in tools when possible vs calling out to external tools and script.
- After modifying a doc, run markdownlint, then review and fix any issues found (using markdownlint ideally).