---
name: how-to-use-auto-k
description: Guide for using Auto-K to build product knowledge graphs and generate agent-ready task specs. Use when starting a new Auto-K project, building graphs, authoring tasks for AI agent dispatch, or when task specs aren't detailed enough for autonomous implementation. Covers the full workflow pipeline, tool usage, and best practices for writing specs that eliminate prompt engineering.
---

# How to Use Auto-K

Auto-K turns unstructured product information into a structured knowledge graph of personas, user stories, requirements, design fragments, and tasks. The graph is the single source of truth — documents and agent briefings are views of the graph.

---

## Getting Started

### 1. Select or Create a Project

```text
project(action='list')           # See existing projects
project(action='set', project_id='...')  # Activate one
project(action='create', name='My Product', description='...')  # Or start fresh
```

### 2. Understand the Schema

```text
get_full_schema()   # All node types, attributes, relations in one call
describe_type('user_story')  # Deep-dive on a specific type
```

### 3. Pick a Workflow

Always follow a workflow — never freestyle graph construction.

| Workflow         | Command                                      | When                                          |
| ---------------- | -------------------------------------------- | --------------------------------------------- |
| PRD Generation   | `read_content('workflows/prd-generation')`   | Starting from scratch, capturing requirements |
| Technical Design | `read_content('workflows/technical-design')` | Have stories, need schemas/APIs/UI specs      |
| Project Planning | `read_content('workflows/project-planning')` | Have designs, need executable tasks           |

**Pipeline:** PRD Generation → Technical Design → Project Planning

---

## The Workflow Pipeline

### Phase 1: PRD Generation

Build the knowledge graph through stakeholder interviews.

**Build order** (each step depends on the previous):

1. **Project goals & metrics** — Why does this product exist? How do we measure success?
2. **Personas** — Who are we building for?
3. **Pains & user goals** — What problems do they face? What do they aspire to?
4. **Epics** — How do features naturally group?
5. **User stories** — What specific actions can each persona take?
6. **Acceptance criteria** — How do we know each story is complete?
7. **Requirements** — What constraints exist (functional, non-functional)?
8. **Risks & mitigations** — What could go wrong?

**Key relations:**

| From       | Relation       | To                  |
| ---------- | -------------- | ------------------- |
| persona    | suffers        | pain                |
| persona    | aspiration     | user_goal           |
| persona    | acts           | user_story          |
| epic       | story_grouping | user_story          |
| user_story | verifies       | acceptance          |
| user_story | satisfies      | requirement         |
| risk       | threatens      | project_goal / epic |
| mitigation | mitigates      | risk                |

### Phase 2: Technical Design

Create design fragments that bridge user stories to implementation.

```text
create_design_fragment(...)  # Schema, API, screen, component specs
```

Design fragments are typed (`schema`, `api`, `screen`, `component`, etc.) and linked to the stories they realize. They contain the actual technical specification — data models, endpoint signatures, UI layouts.

### Phase 3: Project Planning

Break design fragments into executable tasks, organize into milestones, map dependencies.

```text
create_nodes(type="task", ...)
connect_nodes(source="TASK-1", target="DES-1", type="implemented_by")
task(action="add_dep", predecessor_id="TASK-1", successor_id="TASK-2")
```

---

## Tool Reference

### Graph Building

| Tool                     | Purpose                                               |
| ------------------------ | ----------------------------------------------------- |
| `create_nodes`           | Batch create up to 50 nodes (any type)                |
| `edit_node`              | Modify a node (blocked for accepted nodes)            |
| `connect_nodes`          | Create relations between nodes                        |
| `create_design_fragment` | Create typed design specs (schema, api, screen, etc.) |

### Graph Querying

| Tool               | Purpose                                                  |
| ------------------ | -------------------------------------------------------- |
| `search_graph`     | Discovery — compact summaries, filter by type/status     |
| `get_node_details` | Full content — supports bulk fetch with `node_ids=[...]` |
| `get_subgraph`     | Node(s) with neighbors grouped by relation               |
| `get_task_spec`    | Full task briefing for agent dispatch                    |

### Quality & Validation

| Tool                      | Purpose                                                               |
| ------------------------- | --------------------------------------------------------------------- |
| `graph_check`             | Detect anti-patterns: missing acceptance criteria, orphan nodes, etc. |
| `list_check_rules`        | See all available quality rules                                       |
| `task(action='validate')` | Check task dependency graph for cycles, orphans                       |

### Planning & Dependencies

| Tool                        | Purpose                                   |
| --------------------------- | ----------------------------------------- |
| `task(action='add_dep')`    | Create predecessor → successor dependency |
| `task(action='remove_dep')` | Remove a dependency                       |
| `task(action='get_deps')`   | Query predecessors/successors for a task  |
| `task(action='validate')`   | Find cycles, start/end tasks, orphans     |

### Visualization & Export

| Tool                         | Purpose                                                      |
| ---------------------------- | ------------------------------------------------------------ |
| `export_graph_visualization` | Mermaid diagrams (task deps, story coverage, knowledge flow) |

### Content Library

| Tool             | Purpose                                                   |
| ---------------- | --------------------------------------------------------- |
| `browse_content` | List all available content (workflows, templates, guides) |
| `read_content`   | Read a specific content item by shorthand or URI          |

### Sources & Comments

| Tool      | Purpose                                                              |
| --------- | -------------------------------------------------------------------- |
| `source`  | Upload, list, read source documents (inspiration for graph building) |
| `comment` | Discussions on graph nodes (list/create/reply/resolve threads)       |

---

## Writing Agent-Ready Task Specs

The whole point of Auto-K's task system is that `get_task_spec(task_id="TASK-5")` should be the **only briefing an agent needs**. If you have to write a custom prompt beyond "Implement TASK-5", the task spec is incomplete.

### The Goal

```text
Agent prompt: "Implement TASK-5. Codebase: /path/to/repo. Use get_task_spec('TASK-5') for full details."
```

Everything else — files, code, style, done criteria — comes from Auto-K.

### What Makes a Task Spec Agent-Ready

#### 1. Reference Exact File Paths

Bad: "Update the medication view"
Good: "Modify `src/views/MedicationListView.swift` lines 83-92"

Include in the task content:

- Files to read (with why)
- Files to modify (with what section)
- Files to create (with where)

#### 2. Include Runnable Code Snippets

The task content should include **actual code** the agent can adapt, not just descriptions. An agent reading "add a tappable star rating" has to guess the API. An agent reading a code block with the specific framework calls can just adapt and paste.

#### 3. Link Design Fragments to Tasks

Every task should connect to its design fragments via `implemented_by`:

```text
connect_nodes(source="TASK-1", target="DES-3", type="implemented_by")
```

This way `get_task_spec` returns the full design context automatically. Without this, the agent has no idea what the UI/API/schema should look like.

#### 4. Write Specific Done-When Conditions

Create `done_when` nodes with verifiable, specific criteria:

Bad: "Sparkline renders below quick stats"
Good: "SparklineView renders in recentStatsView area, 40pt height, catmullRom interpolation, LineMark + AreaMark, accent stroke, .chartXAxis(.hidden)"

```text
create_nodes(type="done_when", name="SparklineView renders with correct config")
connect_nodes(source="TASK-1", target="DONE-1", type="completes_when")
```

The done-when should be specific enough that an agent can verify its own work.

#### 5. Include Predecessor Context

When TASK-2 depends on TASK-1, the task spec should summarize what TASK-1 produced:

- New methods added (with signatures)
- New properties available
- How to call them

An agent picking up TASK-2 shouldn't need to re-explore the codebase to discover what TASK-1 created.

#### 6. Add Agent-Handoff Metadata

Include in the task content a section for what an AI agent specifically needs:

- Exact imports needed
- Framework/API constraints (e.g., "UICalendarView requires UIViewRepresentable")
- Common pitfalls (e.g., "@Bindable needed for SwiftData mutation")

### Self-Check: Is the Spec Complete?

Before dispatching a task to an agent, verify:

- [ ] File paths are included (read, modify, create)
- [ ] Code snippets show the actual implementation pattern
- [ ] Design fragments are linked (`implemented_by` relations)
- [ ] Done-when conditions are specific and verifiable
- [ ] Predecessor outputs are summarized (if task has dependencies)
- [ ] Agent-specific notes cover imports, constraints, pitfalls

If you need to write more than 4 lines in the agent prompt, the spec needs more detail.

---

## Quality Validation

### Before Finishing Any Phase

```text
graph_check()
```

This catches:

- Stories without acceptance criteria
- Personas without pains or goals
- Orphan nodes (not connected to anything)
- Empty descriptions
- Tasks missing completion conditions
- Design fragments without implementing tasks

### Before Agent Dispatch

```text
task(action='validate')
```

This checks:

- No circular dependencies
- Start tasks exist (can begin immediately)
- End tasks exist (final deliverables)
- No orphan tasks (missing dependency links)

---

## Status Lifecycle

All AI-created nodes start as `proposed`. Only human review changes status to `accepted`. Accepted nodes are protected — `edit_node` rejects modifications.

This means:

- Build the full graph with status `proposed`
- Stakeholders review and accept nodes
- Accepted nodes become the stable foundation for downstream work

---

## Common Patterns

### Batch Creation

Up to 50 nodes per `create_nodes` call. Use this for efficiency:

```text
create_nodes(nodes=[
  {type: "user_story", name: "...", content: "..."},
  {type: "user_story", name: "...", content: "..."},
  ...
])
```

### Progressive Exploration

1. `search_graph(types=["user_story"])` — Get compact summaries
2. `get_node_details(node_ids=["US-1", "US-2"])` — Full content for specific nodes
3. `get_subgraph(node_id="US-1")` — See all connections

### Token Efficiency

- Use `search_graph` first (compact), drill down with `get_node_details` only when needed
- Filter by `types` parameter to narrow results
- Batch operations: up to 50 nodes/edges per call
