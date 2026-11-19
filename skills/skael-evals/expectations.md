# Good and bad expectations

An expectation is a statement the judge marks true or false. It passes only when the transcript or the files show it is true. Write each one so that a good result passes and a plausible bad result fails.

| Bad | Why it fails | Good |
|---|---|---|
| The review is thorough. | Any review looks thorough to someone. Nothing can fail it. | The review identifies the f-string query in get_order as a SQL injection risk. |
| The agent finds the bug. | Which bug? The judge must guess. | The review identifies the end+1 in the slice expression as the cause of the panic. |
| The output is correct. | Correct against what? | report.md gives the March total as 4,210. |
| The summary is good and short and cites sources. | Three statements in one. One failing part fails all three. | Write three expectations, one each. |
| The agent uses the skill. | Measures the trigger, not the work. The trigger set measures that. | Delete it. |
| The agent does not make mistakes. | Unbounded. The judge cannot verify it. | The review does not report a bug that is not in the code. |
| A file named summary.md exists. | Mechanical. A judge adds cost and noise. | A `file_exists` check on summary.md. |
| The tests pass. | The judge cannot run tests. | A `command` check that runs the tests. |
| The agent explains its reasoning. | Rewards length, not results. | The fix in fix.py handles an empty input list. |

Rules:

- One fact per expectation.
- Name the file, the function, the line or the value.
- Make it fail when the work was not really done.
- Grade the result, not the method, unless the method is the point of the skill.
- Write a check when a machine can decide it.
- Keep 2 to 5 expectations and checks per eval. More does not raise precision: each eval counts once in the score.
