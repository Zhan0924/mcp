---
name: excalidraw-diagram
description: Create Excalidraw diagram JSON files that make visual arguments. Use when the user wants to visualize workflows, architectures, or concepts.
---

# Excalidraw Diagram Creator

Generate `.excalidraw` JSON files that **argue visually**, not just display information.

**Setup:** If the user asks you to set up this skill (renderer, dependencies, etc.), see `README.md` for instructions.

## Customization

**All colors and brand-specific styles live in one file:** `references/color-palette.md`. Read it before generating any diagram and use it as the single source of truth for all color choices — shape fills, strokes, text colors, evidence artifact backgrounds, everything.

To make this skill produce diagrams in your own brand style, edit `color-palette.md`. Everything else in this file is universal design methodology and Excalidraw best practices.

---

## Core Philosophy

**Diagrams should ARGUE, not DISPLAY.**

A diagram isn't formatted text. It's a visual argument that shows relationships, causality, and flow that words alone can't express. The shape should BE the meaning.

**The Isomorphism Test**: If you removed all text, would the structure alone communicate the concept? If not, redesign.

**The Education Test**: Could someone learn something concrete from this diagram, or does it just label boxes? A good diagram teaches—it shows actual formats, real event names, concrete examples.

---

## Depth Assessment (Do This First)

Before designing, determine what level of detail this diagram needs:

### Simple/Conceptual Diagrams
Use abstract shapes when:
- Explaining a mental model or philosophy
- The audience doesn't need technical specifics
- The concept IS the abstraction (e.g., "separation of concerns")

### Comprehensive/Technical Diagrams
Use concrete examples when:
- Diagramming a real system, protocol, or architecture
- The diagram will be used to teach or explain (e.g., YouTube video)
- The audience needs to understand what things actually look like
- You're showing how multiple technologies integrate

**For technical diagrams, you MUST include evidence artifacts** (see below).

---

## Research Mandate (For Technical Diagrams)

**Before drawing anything technical, research the actual specifications.**

If you're diagramming a protocol, API, or framework:
1. Look up the actual JSON/data formats
2. Find the real event names, method names, or API endpoints
3. Understand how the pieces actually connect
4. Use real terminology, not generic placeholders

Bad: "Protocol" → "Frontend"
Good: "AG-UI streams events (RUN_STARTED, STATE_DELTA, A2UI_UPDATE)" → "CopilotKit renders via createA2UIMessageRenderer()"

**Research makes diagrams accurate AND educational.**

---

## Evidence Artifacts

Evidence artifacts are concrete examples that prove your diagram is accurate and help viewers learn. Include them in technical diagrams.

**Types of evidence artifacts** (choose what's relevant to your diagram):

| Artifact Type | When to Use | How to Render |
|---------------|-------------|---------------|
| **Code snippets** | APIs, integrations, implementation details | Dark rectangle + syntax-colored text (see color palette for evidence artifact colors) |
| **Data/JSON examples** | Data formats, schemas, payloads | Dark rectangle + colored text (see color palette) |
| **Event/step sequences** | Protocols, workflows, lifecycles | Timeline pattern (line + dots + labels) |
| **UI mockups** | Showing actual output/results | Nested rectangles mimicking real UI |
| **Real input content** | Showing what goes IN to a system | Rectangle with sample content visible |
| **API/method names** | Real function calls, endpoints | Use actual names from docs, not placeholders |

**Example**: For a diagram about a streaming protocol, you might show:
- The actual event names from the spec (not just "Event 1", "Event 2")
- A code snippet showing how to connect
- What the streamed data actually looks like

**Example**: For a diagram about a data transformation pipeline:
- Show sample input data (actual format, not "Input")
- Show sample output data (actual format, not "Output")
- Show intermediate states if relevant

The key principle: **show what things actually look like**, not just what they're called.

---

## Multi-Zoom Architecture

Comprehensive diagrams operate at multiple zoom levels simultaneously. Think of it like a map that shows both the country borders AND the street names.

### Level 1: Summary Flow
A simplified overview showing the full pipeline or process at a glance. Often placed at the top or bottom of the diagram.

*Example*: `Input → Processing → Output` or `Client → Server → Database`

### Level 2: Section Boundaries
Labeled regions that group related components. These create visual "rooms" that help viewers understand what belongs together.

*Example*: Grouping by responsibility (Backend / Frontend), by phase (Setup / Execution / Cleanup), or by team (User / System / External)

### Level 3: Detail Inside Sections
Evidence artifacts, code snippets, and concrete examples within each section. This is where the educational value lives.

*Example*: Inside a "Backend" section, you might show the actual API response format, not just a box labeled "API Response"

### Level 4: Module Internals (Module Drill-Down)
When a module is complex enough (3+ internal steps, branching logic, or state transitions), **expand it** into a detailed sub-diagram showing its internal flow, decision points, and data transformations.

*Example*: Instead of just a box labeled "Query Processor", expand it to show: `Validate Input → Build Embedding → Search Vector DB → Re-rank Results → Return Top-K`

**Expansion strategy:**

| Condition | Strategy |
|-----------|----------|
| Total modules ≤ 3 AND each module's internal steps ≤ 8 | **Single-canvas expansion** — expand within the same .excalidraw using Frame elements |
| Total modules > 3 OR any module has > 8 steps | **Multi-file expansion** — generate `overview.excalidraw` + `module-xxx.excalidraw` per module |
| User explicitly requests "one diagram for everything" | Single-canvas (but warn about potential crowding) |

### Code Path Annotations (MANDATORY for Technical Diagrams)

**Every module in a technical diagram MUST show its related source code paths directly below the module.** This is not optional — it bridges the gap between architecture diagrams and actual code.

**Rules:**
- Place code paths as free-floating text directly below each module box
- Use `fontSize: 12`, `fontFamily: 3` (monospace), and the Body/Detail color from the palette
- Format as relative file paths: `rag/retriever.go`, `tools/rag_tools.go`
- If a module spans multiple files, list all relevant files, one per line
- For expanded modules (Level 4), also annotate each internal step with its specific function or method name

**Example layout:**
```
┌─────────────────────┐
│   Query Processor    │
└─────────────────────┘
  rag/retriever.go
  rag/retriever_reranker.go
```

**For expanded module internals:**
```
┌─ Module: Query Processor ─────────────────────────┐
│  ┌──────────┐    ┌──────────┐    ┌──────────┐     │
│  │ Validate  │ →  │ Embed    │ →  │ Re-rank  │     │
│  └──────────┘    └──────────┘    └──────────┘     │
│  Validate()       GetEmbedding()  Rerank()         │
│  retriever.go     embedding.go    reranker.go      │
└───────────────────────────────────────────────────┘
```

**For comprehensive diagrams, aim to include all four levels.** The summary gives context, the sections organize, the details teach, and the module internals reveal how things actually work. Code paths ground everything in the real codebase.

### Bad vs Good

| Bad (Displaying) | Good (Arguing) |
|------------------|----------------|
| 5 equal boxes with labels | Each concept has a shape that mirrors its behavior |
| Card grid layout | Visual structure matches conceptual structure |
| Icons decorating text | Shapes that ARE the meaning |
| Same container for everything | Distinct visual vocabulary per concept |
| Everything in a box | Free-floating text with selective containers |

### Simple vs Comprehensive (Know Which You Need)

| Simple Diagram | Comprehensive Diagram |
|----------------|----------------------|
| Generic labels: "Input" → "Process" → "Output" | Specific: shows what the input/output actually looks like |
| Named boxes: "API", "Database", "Client" | Named boxes + examples of actual requests/responses |
| "Events" or "Messages" label | Timeline with real event/message names from the spec |
| "UI" or "Dashboard" rectangle | Mockup showing actual UI elements and content |
| ~30 seconds to explain | ~2-3 minutes of teaching content |
| Viewer learns the structure | Viewer learns the structure AND the details |

**Simple diagrams** are fine for abstract concepts, quick overviews, or when the audience already knows the details. **Comprehensive diagrams** are needed for technical architectures, tutorials, educational content, or when you want the diagram itself to teach.

---

## Container vs. Free-Floating Text

**Not every piece of text needs a shape around it.** Default to free-floating text. Add containers only when they serve a purpose.

| Use a Container When... | Use Free-Floating Text When... |
|------------------------|-------------------------------|
| It's the focal point of a section | It's a label or description |
| It needs visual grouping with other elements | It's supporting detail or metadata |
| Arrows need to connect to it | It describes something nearby |
| The shape itself carries meaning (decision diamond, etc.) | Typography alone creates sufficient hierarchy |
| It represents a distinct "thing" in the system | It's a section title, subtitle, or annotation |

**Typography as hierarchy**: Use font size, weight, and color to create visual hierarchy without boxes. A 28px title doesn't need a rectangle around it.

**The container test**: For each boxed element, ask "Would this work as free-floating text?" If yes, remove the container.

---

## Design Process (Do This BEFORE Generating JSON)

### Step 0: Assess Depth Required
Before anything else, determine if this needs to be:
- **Simple/Conceptual**: Abstract shapes, labels, relationships (mental models, philosophies)
- **Comprehensive/Technical**: Concrete examples, code snippets, real data (systems, architectures, tutorials)

**If comprehensive**: Do research first. Look up actual specs, formats, event names, APIs.

### Step 1: Understand Deeply
Read the content. For each concept, ask:
- What does this concept **DO**? (not what IS it)
- What relationships exist between concepts?
- What's the core transformation or flow?
- **What would someone need to SEE to understand this?** (not just read about)

### Step 1.5: Module Depth Scan (For Technical Diagrams)
For each identified module/component, evaluate whether it needs internal expansion:

| Question | If YES → |
|----------|----------|
| Does this module have 3+ internal steps? | Needs expansion |
| Does it contain conditional branching? | Show decision diamonds inside |
| Does it have state transitions? | Use state machine pattern |
| Does it interact with external systems (DB, API, cache)? | Show I/O boundaries |
| Does it have error handling / retry logic? | Show loop-back arrows |
| Does data transform inside this module? | Show before/after data examples |

For each module that "needs expansion", select a **Module Internal Pattern** (see below in Visual Pattern Library).

**Also identify code paths**: For every module, find the actual source files and key function names that implement it. These will be annotated on the diagram.

### Step 2: Map Concepts to Patterns
For each concept, find the visual pattern that mirrors its behavior:

| If the concept... | Use this pattern |
|-------------------|------------------|
| Spawns multiple outputs | **Fan-out** (radial arrows from center) |
| Combines inputs into one | **Convergence** (funnel, arrows merging) |
| Has hierarchy/nesting | **Tree** (lines + free-floating text) |
| Is a sequence of steps | **Timeline** (line + dots + free-floating labels) |
| Loops or improves continuously | **Spiral/Cycle** (arrow returning to start) |
| Is an abstract state or context | **Cloud** (overlapping ellipses) |
| Transforms input to output | **Assembly line** (before → process → after) |
| Compares two things | **Side-by-side** (parallel with contrast) |
| Separates into phases | **Gap/Break** (visual separation between sections) |

### Step 3: Ensure Variety
For multi-concept diagrams: **each major concept must use a different visual pattern**. No uniform cards or grids.

### Step 4: Sketch the Flow
Before JSON, mentally trace how the eye moves through the diagram. There should be a clear visual story.

### Step 5: Generate JSON
Only now create the Excalidraw elements. **See below for how to handle large diagrams.**

### Step 6: Render & Validate (MANDATORY)
After generating the JSON, you MUST run the render-view-fix loop until the diagram looks right. This is not optional — see the **Render & Validate** section below for the full process.

---

## Large / Comprehensive Diagram Strategy

**For comprehensive or technical diagrams, you MUST build the JSON one section at a time.** Do NOT attempt to generate the entire file in a single pass. This is a hard constraint — Claude Code has a ~32,000 token output limit per response, and a comprehensive diagram easily exceeds that in one shot. Even if it didn't, generating everything at once leads to worse quality. Section-by-section is better in every way.

### The Section-by-Section Workflow

**Phase 1: Build each section**

1. **Create the base file** with the JSON wrapper (`type`, `version`, `appState`, `files`) and the first section of elements.
2. **Add one section per edit.** Each section gets its own dedicated pass — take your time with it. Think carefully about the layout, spacing, and how this section connects to what's already there.
3. **Use descriptive string IDs** (e.g., `"trigger_rect"`, `"arrow_fan_left"`) so cross-section references are readable.
4. **Namespace seeds by section** (e.g., section 1 uses 100xxx, section 2 uses 200xxx) to avoid collisions.
5. **Update cross-section bindings** as you go. When a new section's element needs to bind to an element from a previous section (e.g., an arrow connecting sections), edit the earlier element's `boundElements` array at the same time.

**Phase 1.5: Expand module internals**

For each module marked "needs expansion" in Step 1.5:
1. **Determine expansion placement** — right side of the overview, below it, or in a separate file (based on the expansion strategy table in Level 4).
2. **Draw the connection** — From the module box in the overview, add a dashed arrow to the expansion area with a label like "Internal Detail ↓" or use matching numbered badges (①②③).
3. **Build the expansion** using the appropriate Module Internal Pattern (Internal Pipeline, Decision Tree, State Machine, etc.).
4. **Use depth-scaled sizing** — Internal elements should be one size level smaller than the overview:
   - Overview uses Primary (180×90) → Internal uses Secondary (120×60)
   - Overview uses Secondary (120×60) → Internal uses Small (80×50)
5. **Annotate code paths** — Below each internal step, place function name + file path as free-floating text (`fontSize: 12`).
6. **Use depth-level colors** — Internal elements use the L2/L3 depth colors from the palette to visually distinguish them from the overview level.

**Phase 2: Review the whole**

After all sections are in place, read through the complete JSON and check:
- Are cross-section arrows bound correctly on both ends?
- Is the overall spacing balanced, or are some sections cramped while others have too much whitespace?
- Do IDs and bindings all reference elements that actually exist?

Fix any alignment or binding issues before rendering.

**Phase 3: Render & validate**

Now run the render-view-fix loop from the Render & Validate section. This is where you'll catch visual issues that aren't obvious from JSON — overlaps, clipping, imbalanced composition.

### Section Boundaries

Plan your sections around natural visual groupings from the diagram plan. A typical large diagram might split into:

- **Section 1**: Entry point / trigger
- **Section 2**: First decision or routing
- **Section 3**: Main content (hero section — may be the largest single section)
- **Section 4-N**: Remaining phases, outputs, etc.

Each section should be independently understandable: its elements, internal arrows, and any cross-references to adjacent sections.

### What NOT to Do

- **Don't generate the entire diagram in one response.** You will hit the output token limit and produce truncated, broken JSON. Even if the diagram is small enough to fit, splitting into sections produces better results.
- **Don't use a coding agent** to generate the JSON. The agent won't have sufficient context about the skill's rules, and the coordination overhead negates any benefit.
- **For small diagrams (< 50 elements), prefer hand-crafted JSON** with descriptive IDs — it's more maintainable and easier to debug.
- **For large diagrams (50+ elements), a Python generator script is acceptable and often necessary.** When using a script:
  - Define edge-midpoint helpers (`top()`, `bot()`, `lft()`, `rgt()`) — see `references/element-templates.md` "Element Edge Connection Points"
  - Have `add_arrow(id, x1, y1, x2, y2, color)` accept absolute coordinates and compute relative `points` internally
  - Return `(x, y, w, h)` tuples from shape creation functions so arrows can reference element bounds
  - Validate that every arrow starts from an element's edge, not an arbitrary point

---

## Visual Pattern Library

### Fan-Out (One-to-Many)
Central element with arrows radiating to multiple targets. Use for: sources, PRDs, root causes, central hubs.
```
        ○
       ↗
  □ → ○
       ↘
        ○
```

### Convergence (Many-to-One)
Multiple inputs merging through arrows to single output. Use for: aggregation, funnels, synthesis.
```
  ○ ↘
  ○ → □
  ○ ↗
```

### Tree (Hierarchy)
Parent-child branching with connecting lines and free-floating text (no boxes needed). Use for: file systems, org charts, taxonomies.
```
  label
  ├── label
  │   ├── label
  │   └── label
  └── label
```
Use `line` elements for the trunk and branches, free-floating text for labels.

### Spiral/Cycle (Continuous Loop)
Elements in sequence with arrow returning to start. Use for: feedback loops, iterative processes, evolution.
```
  □ → □
  ↑     ↓
  □ ← □
```

### Cloud (Abstract State)
Overlapping ellipses with varied sizes. Use for: context, memory, conversations, mental states.

### Assembly Line (Transformation)
Input → Process Box → Output with clear before/after. Use for: transformations, processing, conversion.
```
  ○○○ → [PROCESS] → □□□
  chaos              order
```

### Side-by-Side (Comparison)
Two parallel structures with visual contrast. Use for: before/after, options, trade-offs.

### Gap/Break (Separation)
Visual whitespace or barrier between sections. Use for: phase changes, context resets, boundaries.

### Lines as Structure
Use lines (type: `line`, not arrows) as primary structural elements instead of boxes:
- **Timelines**: Vertical or horizontal line with small dots (10-20px ellipses) at intervals, free-floating labels beside each dot
- **Tree structures**: Vertical trunk line + horizontal branch lines, with free-floating text labels (no boxes needed)
- **Dividers**: Thin dashed lines to separate sections
- **Flow spines**: A central line that elements relate to, rather than connecting boxes

```
Timeline:           Tree:
  ●─── Label 1        │
  │                   ├── item
  ●─── Label 2        │   ├── sub
  │                   │   └── sub
  ●─── Label 3        └── item
```

Lines + free-floating text often creates a cleaner result than boxes + contained text.

### Module Internal Patterns (For Level 4 Drill-Down)

Use these patterns when expanding a module to show its internal workings. Each pattern is designed for a specific type of internal behavior.

#### Internal Pipeline
A linear chain of sub-steps inside a module container. Use when the module processes data through sequential stages.
```
┌─ Module Name ──────────────────────────────────────┐
│  ┌────────┐    ┌────────┐    ┌────────┐           │
│  │ Step 1 │ →  │ Step 2 │ →  │ Step 3 │           │
│  └────────┘    └────────┘    └────────┘           │
│  FuncA()        FuncB()        FuncC()             │
│  file_a.go      file_b.go      file_c.go          │
└────────────────────────────────────────────────────┘
```
Use smaller rectangles (80×50) inside a Frame container. Annotate each step with function name and file path below it.

#### Internal Decision Tree
A branching flow inside a module. Use when the module has conditional logic that routes to different processing paths.
```
┌─ Module Name ────────────────────────────────┐
│              ◇ Condition?                     │
│            ↙     ↘                            │
│    ┌──────┐     ┌──────┐                      │
│    │Path A│     │Path B│                       │
│    └──────┘     └──────┘                      │
│    handleA()    handleB()                      │
└──────────────────────────────────────────────┘
```
Use diamond for decision, small rectangles for branches. Show the actual condition (not "if true").

#### State Machine
Connected states with labeled transitions. Use when the module manages lifecycle states or workflow states.
```
┌─ Module Name ──────────────────────────────────┐
│  ╭──────╮  event_a   ╭──────╮  event_b        │
│  │ Idle │ ─────────→ │Active│ ────────→ Done   │
│  ╰──────╯   ←─────── ╰──────╯                 │
│              timeout                            │
│  state.go                                      │
└────────────────────────────────────────────────┘
```
Use rounded rectangles for states. Label every transition arrow with the trigger event/condition.

#### Transform Chain
Shows data changing shape as it flows through the module. Use when the module's purpose is data transformation.
```
┌─ Module Name ──────────────────────────────────────┐
│  ┌─────────┐    ┌─────────┐    ┌─────────┐        │
│  │ Input   │    │ Middle  │    │ Output  │         │
│  │ {"raw":  │ →  │ chunks: │ →  │ vectors:│         │
│  │  "text"} │    │ ["a"..] │    │ [0.1..] │         │
│  └─────────┘    └─────────┘    └─────────┘        │
│  parser.go       chunker.go     embedder.go        │
└────────────────────────────────────────────────────┘
```
Use dark-background evidence artifacts to show actual data at each stage.

#### Read-Write Pattern
Bidirectional interaction with storage. Use when the module reads from and/or writes to databases, caches, or external stores.
```
┌─ Module Name ────────────────────┐
│  ┌──────────┐    ╭──────────╮    │
│  │ Process  │ ⟺  │ Storage  │    │
│  └──────────┘    ╰──────────╯    │
│  get/set keys     Redis/Milvus   │
│  store.go                        │
└──────────────────────────────────┘
```
Use cylinder or rounded shape for storage. Double-headed arrow for read-write.

#### Retry Loop
A process with error handling and retry logic. Use when the module has fallback or retry behavior.
```
┌─ Module Name ─────────────────────────────────┐
│  ┌────────┐    ┌────────┐    ┌────────┐      │
│  │ Try    │ →  │ Check  │ →  │ Return │      │
│  └────────┘    └────────┘    └────────┘      │
│       ↑          │ error                      │
│       └──────────┘ retry (max 3)              │
│  retry.go                                     │
└───────────────────────────────────────────────┘
```
Use a return arrow from error check back to the try step, with retry condition as label.

#### Parallel Lanes
Multiple concurrent processing paths. Use when the module processes things in parallel (goroutines, workers, etc.).
```
┌─ Module Name ─────────────────────────────────┐
│  ┌─ Lane 1 ────────────────────────┐          │
│  │  Worker → Process → Collect     │          │
│  └─────────────────────────────────┘          │
│  ┌─ Lane 2 ────────────────────────┐          │
│  │  Worker → Process → Collect     │   → Merge│
│  └─────────────────────────────────┘          │
│  ┌─ Lane 3 ────────────────────────┐          │
│  │  Worker → Process → Collect     │          │
│  └─────────────────────────────────┘          │
│  worker_pool.go                               │
└───────────────────────────────────────────────┘
```
Use horizontal sub-frames for each lane, converging to a single merge point.

---

## Shape Meaning

Choose shape based on what it represents—or use no shape at all:

| Concept Type | Shape | Why |
|--------------|-------|-----|
| Labels, descriptions, details | **none** (free-floating text) | Typography creates hierarchy |
| Section titles, annotations | **none** (free-floating text) | Font size/weight is enough |
| Markers on a timeline | small `ellipse` (10-20px) | Visual anchor, not container |
| Start, trigger, input | `ellipse` | Soft, origin-like |
| End, output, result | `ellipse` | Completion, destination |
| Decision, condition | `diamond` | Classic decision symbol |
| Process, action, step | `rectangle` | Contained action |
| Abstract state, context | overlapping `ellipse` | Fuzzy, cloud-like |
| Hierarchy node | lines + text (no boxes) | Structure through lines |

**Rule**: Default to no container. Add shapes only when they carry meaning. Aim for <30% of text elements to be inside containers.

---

## Color as Meaning

Colors encode information, not decoration. Every color choice should come from `references/color-palette.md` — the semantic shape colors, text hierarchy colors, and evidence artifact colors are all defined there.

**Key principles:**
- Each semantic purpose (start, end, decision, AI, error, etc.) has a specific fill/stroke pair
- Free-floating text uses color for hierarchy (titles, subtitles, details — each at a different level)
- Evidence artifacts (code snippets, JSON examples) use their own dark background + colored text scheme
- Always pair a darker stroke with a lighter fill for contrast

**Do not invent new colors.** If a concept doesn't fit an existing semantic category, use Primary/Neutral or Secondary.

---

## Modern Aesthetics

For clean, professional diagrams:

### Roughness
- `roughness: 0` — Clean, crisp edges. Use for modern/technical diagrams.
- `roughness: 1` — Hand-drawn, organic feel. Use for brainstorming/informal diagrams.

**Default to 0** for most professional use cases.

### Stroke Width
- `strokeWidth: 1` — Thin, elegant. Good for lines, dividers, subtle connections.
- `strokeWidth: 2` — Standard. Good for shapes and primary arrows.
- `strokeWidth: 3` — Bold. Use sparingly for emphasis (main flow line, key connections).

### Opacity
**Always use `opacity: 100` for all elements.** Use color, size, and stroke width to create hierarchy instead of transparency.

### Small Markers Instead of Shapes
Instead of full shapes, use small dots (10-20px ellipses) as:
- Timeline markers
- Bullet points
- Connection nodes
- Visual anchors for free-floating text

---

## Layout Principles

### Hierarchy Through Scale
- **Hero**: 300×150 - visual anchor, most important
- **Primary**: 180×90
- **Secondary**: 120×60
- **Small**: 60×40

### Whitespace = Importance
The most important element has the most empty space around it (200px+).

### Flow Direction
Guide the eye: typically left→right or top→bottom for sequences, radial for hub-and-spoke.

### Connections Required
Position alone doesn't show relationships. If A relates to B, there must be an arrow.

### Minimum Spacing Guidelines

**These are hard minimums — violating them causes visual overlaps.**

| Context | Minimum Distance | Recommended |
|---------|-----------------|-------------|
| **Vertical gap** between sequential boxes | 50px | 60px |
| **Vertical gap** between box and diamond | 50px | 60px |
| **Horizontal gap** between parallel columns | 300px | 400px+ |
| **Label offset** from parent element | 8px | 10-12px |
| **Bypass arrow channel** (detour around elements) | 40px from nearest element edge | 50px |
| **Diamond to first branch element** | 30px below branch routing line | 40px |
| **Merge point clearance** (arrows converging) | 10px above target element | 15px |

**Column planning**: For N parallel vertical flows, allocate at least `N × 400px` of total width. Example: 3-column layout → minimum 1200px canvas width.

**Box sizing**: Boxes containing 2-line text need at minimum `width: 180px, height: 52px`. For single-line text: `width: 140px, height: 40px`.

### Overview ↔ Detail Connection (For Module Drill-Down)

When modules are expanded to show internal details, there MUST be a clear visual connection between the overview module and its expansion:

| Connection Method | When to Use | How |
|-------------------|-------------|-----|
| **Dashed arrow + label** | Single-canvas expansion | Dashed arrow from overview module → expansion Frame, labeled "Internal Detail ↓" |
| **Numbered badges** | Multiple expansions on same canvas | Overview module shows ① , expansion area titled "① Module Name Internal" |
| **Color matching** | Always (supplement other methods) | Overview module border color = expansion Frame border color |
| **File naming** | Multi-file expansion | `overview.excalidraw` + `module-retriever.excalidraw` with matching names |

**Prohibition**: An expansion area MUST NOT exist without a visual or naming link back to its parent module. The viewer must never wonder "what does this detail section belong to?"

---

## Text Rules

**CRITICAL**: The JSON `text` property contains ONLY readable words.

```json
{
  "id": "myElement1",
  "text": "Start",
  "originalText": "Start"
}
```

Settings: `fontSize: 16`, `fontFamily: 3`, `textAlign: "center"`, `verticalAlign: "middle"`

---

## JSON Structure

```json
{
  "type": "excalidraw",
  "version": 2,
  "source": "https://excalidraw.com",
  "elements": [...],
  "appState": {
    "viewBackgroundColor": "#ffffff",
    "gridSize": 20
  },
  "files": {}
}
```

## Element Templates

See `references/element-templates.md` for copy-paste JSON templates for each element type (text, line, dot, rectangle, arrow). Pull colors from `references/color-palette.md` based on each element's semantic purpose.

---

## Render & Validate (MANDATORY)

You cannot judge a diagram from JSON alone. After generating or editing the Excalidraw JSON, you MUST render it to PNG, view the image, and fix what you see — in a loop until it's right. This is a core part of the workflow, not a final check.

### How to Render

```bash
cd .claude/skills/excalidraw-diagram/references && uv run python render_excalidraw.py <path-to-file.excalidraw>
```

This outputs a PNG next to the `.excalidraw` file. Then use the **Read tool** on the PNG to actually view it.

### The Loop

After generating the initial JSON, run this cycle:

**1. Render & View** — Run the render script, then Read the PNG.

**2. Audit against your original vision** — Before looking for bugs, compare the rendered result to what you designed in Steps 1-4. Ask:
- Does the visual structure match the conceptual structure you planned?
- Does each section use the pattern you intended (fan-out, convergence, timeline, etc.)?
- Does the eye flow through the diagram in the order you designed?
- Is the visual hierarchy correct — hero elements dominant, supporting elements smaller?
- For technical diagrams: are the evidence artifacts (code snippets, data examples) readable and properly placed?

**3. Check for visual defects:**
- Text clipped by or overflowing its container
- Text or shapes overlapping other elements
- Arrows crossing through elements instead of routing around them
- Arrows landing on the wrong element or pointing into empty space
- Labels floating ambiguously (not clearly anchored to what they describe)
- Uneven spacing between elements that should be evenly spaced
- Sections with too much whitespace next to sections that are too cramped
- Text too small to read at the rendered size
- Overall composition feels lopsided or unbalanced

**4. Fix** — Edit the JSON to address everything you found. Common fixes:
- Widen containers when text is clipped
- Adjust `x`/`y` coordinates to fix spacing and alignment
- Add intermediate waypoints to arrow `points` arrays to route around elements
- Reposition labels closer to the element they describe
- Resize elements to rebalance visual weight across sections

**5. Re-render & re-view** — Run the render script again and Read the new PNG.

**6. Repeat** — Keep cycling until the diagram passes both the vision check (Step 2) and the defect check (Step 3). Typically takes 2-4 iterations. Don't stop after one pass just because there are no critical bugs — if the composition could be better, improve it.

### When to Stop

The loop is done when:
- The rendered diagram matches the conceptual design from your planning steps
- No text is clipped, overlapping, or unreadable
- Arrows route cleanly and connect to the right elements
- Spacing is consistent and the composition is balanced
- You'd be comfortable showing it to someone without caveats

### First-Time Setup
If the render script hasn't been set up yet:
```bash
cd .claude/skills/excalidraw-diagram/references
uv sync
uv run playwright install chromium
```

---

## Quality Checklist

### Depth & Evidence (Check First for Technical Diagrams)
1. **Research done**: Did you look up actual specs, formats, event names?
2. **Evidence artifacts**: Are there code snippets, JSON examples, or real data?
3. **Multi-zoom**: Does it have summary flow + section boundaries + detail + module internals?
4. **Concrete over abstract**: Real content shown, not just labeled boxes?
5. **Educational value**: Could someone learn something concrete from this?

### Module Detail (For Technical Diagrams with Expanded Modules)
6. **Module depth scan done**: Every module evaluated for expansion need?
7. **Internal pattern matched**: Each expanded module uses an appropriate Internal Pattern?
8. **Visual connection**: Every expansion area linked back to its overview module (dashed arrow / number / color)?
9. **Depth-scaled sizing**: Internal elements are one size level smaller than overview elements?
10. **Code paths annotated**: Every module shows its source file paths below it?
11. **Function names shown**: Expanded module steps show actual function/method names?
12. **Not over-expanded**: Simple modules (≤2 steps) are NOT forcefully expanded?

### Conceptual
6. **Isomorphism**: Does each visual structure mirror its concept's behavior?
7. **Argument**: Does the diagram SHOW something text alone couldn't?
8. **Variety**: Does each major concept use a different visual pattern?
9. **No uniform containers**: Avoided card grids and equal boxes?

### Container Discipline
10. **Minimal containers**: Could any boxed element work as free-floating text instead?
11. **Lines as structure**: Are tree/timeline patterns using lines + text rather than boxes?
12. **Typography hierarchy**: Are font size and color creating visual hierarchy (reducing need for boxes)?

### Structural
13. **Connections**: Every relationship has an arrow or line
14. **Flow**: Clear visual path for the eye to follow
15. **Hierarchy**: Important elements are larger/more isolated

### Technical
16. **Text clean**: `text` contains only readable words
17. **Font**: `fontFamily: 3`
18. **Roughness**: `roughness: 0` for clean/modern (unless hand-drawn style requested)
19. **Opacity**: `opacity: 100` for all elements (no transparency)
20. **Container ratio**: <30% of text elements should be inside containers

### Arrow Connection Integrity (Check BEFORE Rendering)
21. **Every arrow starts on an element edge**: Verify `(x, y)` matches a `top()/bot()/lft()/rgt()` of a source element — never an arbitrary point in space
22. **Every arrow ends on an element edge**: Verify `(x + points[last][0], y + points[last][1])` lands on a target element's edge midpoint
23. **Multi-branch diamonds use distinct edges**: For 2 branches use left + right; for 3 branches use left + right + bottom; never two arrows from the same edge going to different targets
24. **Minimum spacing respected**: All gaps between elements ≥ 50px vertical, ≥ 300px horizontal between columns

### Visual Validation (Render Required — DO NOT SKIP)
25. **Rendered to PNG**: Diagram has been rendered and visually inspected
26. **No text overflow**: All text fits within its container
27. **No overlapping elements**: Shapes and text don't overlap unintentionally
28. **Even spacing**: Similar elements have consistent spacing
29. **Arrows land correctly**: Arrows connect to intended elements without crossing others
30. **Readable at export size**: Text is legible in the rendered PNG
31. **Balanced composition**: No large empty voids or overcrowded regions
