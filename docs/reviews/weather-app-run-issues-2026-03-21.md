# Weather App Pipeline Run Issue Log

## Summary
- Project: Weather App (FastAPI + React/Vite + Open-Meteo)
- Run window: 2026-03-21 to 2026-03-22
- Primary repos involved: `C:\Users\n8t0\source\weather-app` and `C:\Users\n8t0\source\weather-app-fresh`
- Outcome at checkpoint: Fresh restart recovered the run; bootstrap, backend, and frontend tasks advanced, with `add-search-states-and-persistence-v2` active at the latest state review.
- Manual interventions: Multiple. The run required repo relocation, a full fresh restart, and a direct blackboard repair for a wedged approved task.

## Issue Log

### Issue #1: Missing integration branch blocked coder execution
- Time: Approx. early in the original `weather-app` attempt on 2026-03-21
- Category: setup / intervention
- Agent: coder
- Task: Initial coding tasks in original `weather-app`
- Description: Coder runs failed because the repo did not have the expected `integration` branch and did not yet have the git baseline Liza expects.
- Root cause: The repo was launched before the branch/layout assumptions in `.liza/state.yaml` matched the actual git state.
- Action taken: Diagnosed the mismatch, created or aligned the repo with an initial commit on `integration`, and retried the run.
- Impact: Early pipeline attempts failed before task execution could proceed cleanly.

### Issue #2: OneDrive-backed repo caused `.liza` state drift and lock risk
- Time: Repeated across the original `weather-app` run on 2026-03-21
- Category: infrastructure / intervention
- Agent: multiple
- Task: Blackboard state management
- Description: `.liza/state.yaml` and related runtime state repeatedly drifted, and the repo showed symptoms consistent with file-sync interference while agents were operating.
- Root cause: Running the active repo inside OneDrive created a fragile environment for frequently updated runtime files.
- Action taken: Moved the app repo out of OneDrive and later abandoned the unstable repo in favor of a new fresh repo under `C:\Users\n8t0\source`.
- Impact: State reliability was poor, diagnosis consumed operator time, and confidence in the old blackboard was lost.

### Issue #3: STOPPED-mode aborts looked like crashes during diagnosis
- Time: During the original `weather-app` debugging cycle on 2026-03-21
- Category: observability / operator confusion
- Agent: multiple
- Task: Various
- Description: Agents emitted `ABORT signal received` behavior while the blackboard mode was effectively stopped, which looked like agent failure until correlated with the run mode.
- Root cause: The runtime state and operator mental model diverged; agents were being inspected as though they should continue working when the control plane had already stopped them.
- Action taken: Reviewed state and logs to separate intentional abort behavior from true crashes.
- Impact: Delayed root-cause identification and complicated restart decisions.

### Issue #4: Runtime `.liza` artifacts kept participating in review and merge friction
- Time: Original `weather-app` run before fresh restart
- Category: workflow / review regression
- Agent: coder / reviewer
- Task: Multiple remediation and bootstrap tasks
- Description: Generated `.liza` runtime files repeatedly became part of the working tree/review surface, contributing to noisy diffs and avoidable review churn.
- Root cause: Runtime artifact hygiene was not stable enough in the in-progress app repo during active orchestration.
- Action taken: Tightened `.gitignore` handling in the fresh repo and restarted from scaffold-only contents to remove accumulated runtime noise.
- Impact: Review cycles spent time on generated-state concerns instead of application work.

### Issue #5: Original run became unreliable enough to require a full fresh restart
- Time: 2026-03-21, after repeated state repairs in `weather-app`
- Category: intervention
- Agent: all
- Task: Entire sprint
- Description: The original app repo accumulated enough state drift, path confusion, and blackboard inconsistency that continuing in place was no longer trustworthy.
- Root cause: Combined effects of branch/bootstrap mismatch, OneDrive interference, and repeated runtime-state churn.
- Action taken: Created `C:\Users\n8t0\source\weather-app-fresh` from the clean scaffold only, reinitialized git/Liza, and restarted the run on a new `integration` branch.
- Impact: Previous runtime state was discarded; recovery required a full restart even though useful code work had to be preserved separately.

### Issue #6: Approved task wedged because the worktree was missing
- Time: Before the later fresh-run recovery, observed in `weather-app-fresh` on 2026-03-22
- Category: merge / blackboard inconsistency
- Agent: code-reviewer / coder
- Task: `bootstrap-hosting-foundation-v2`
- Description: The task reached `APPROVED`, but merge/retry attempts failed because the expected task worktree was missing, leaving the task stuck even though the approved commit was already effectively in integration history.
- Root cause: Blackboard task state still expected a worktree-mediated merge path after the worktree had disappeared.
- Action taken: Verified the approved commit was already an ancestor of `integration`, reran the task verification commands successfully, and manually repaired `.liza/state.yaml` to mark the task `MERGED`.
- Impact: Agents stalled until a direct state repair was performed.

### Issue #7: Blackboard state lagged git reality after recovery
- Time: Same recovery window as Issue #6 on 2026-03-22
- Category: state consistency / manual repair
- Agent: supervisor state
- Task: `bootstrap-hosting-foundation-v2`
- Description: The blackboard still represented the task as not merged even after the effective merge condition had already been satisfied in git.
- Root cause: State progression depended on a merge path that no longer matched repo reality after the worktree failure.
- Action taken: Reconciled blackboard state against git ancestry and verification results, then updated the task status manually.
- Impact: This was a direct manual intervention on orchestrator state, which should normally be avoided.

### Issue #8: Fresh-run agents hit runtime budget and deregistered mid-sprint
- Time: 2026-03-22 01:30Z to 01:37Z
- Category: budget / interruption
- Agent: coder and code-reviewer
- Task: Transition from `implement-backend-weather-api-v2` to `build-frontend-weather-ui-v2`
- Description: Event history shows both coder and reviewer emitted 80% budget warnings and then exceeded the 1-hour runtime budget, deregistering while work was still in progress.
- Root cause: Runtime budget was too short for the restarted sprint sequence and recovery overhead.
- Action taken: Agents were restarted, after which `add-search-states-and-persistence-v2` was claimed and the run continued.
- Impact: Progress paused and required operator restart even though the task chain itself remained healthy.

### Issue #9: Sprint metrics became inconsistent with live task state
- Time: Latest fresh-run state inspection on 2026-03-22
- Category: observability
- Agent: reporting layer
- Task: Sprint metrics
- Description: The sprint metrics reported `tasks_in_progress: 0` while the task list and agent section showed `add-search-states-and-persistence-v2` as `IMPLEMENTING` with `coder-1` actively working it.
- Root cause: Metrics/reporting lagged behind the authoritative task and agent state.
- Action taken: Interpreted current activity using task status plus agent `current_task` rather than relying on the aggregate metrics block.
- Impact: Dashboard summaries can mislead operators during live diagnosis.

### Issue #10: Historical `assigned_to` fields made active-claim inspection ambiguous
- Time: Latest fresh-run state review on 2026-03-22
- Category: observability
- Agent: task inspection
- Task: Multiple merged v2 tasks
- Description: Several merged tasks retained `assigned_to: coder-1`, which made it look like multiple tasks might still be claimed until the status fields were reviewed carefully.
- Root cause: The state model preserves assignment history on merged tasks without clearly distinguishing historical ownership from live claims.
- Action taken: Confirmed active work by using `status: IMPLEMENTING` and the `agents` section instead of `assigned_to` alone.
- Impact: Increased operator uncertainty during blackboard review.

## Key Takeaways

1. Repo placement matters. Running active Liza state under OneDrive remained a practical reliability problem.
2. Blackboard recovery still needs a safer path when an approved task loses its worktree but the approved commit is already reachable from `integration`.
3. Dashboard metrics and historical task fields are not sufficient by themselves for live diagnosis; the task status and agent sections are more trustworthy.
4. The fresh restart was the right recovery decision. After the reset, the run progressed materially and the active chain advanced into the final frontend persistence task.