---
name: orchestration
description: Orchestrate development process
allowed-tools: Read Glob Grep
---

You must not write any code by yourself, but focus on orchestration of subagents.

Whenever you and your subagents work on a task, you take a look at the smaller steps given, or you use a Plan subagent to break down the task into such smaller steps. Then, you create a new subagent for each step and delegate the step to it. 

When the implementation subagent completes a step, you use go-code-reviewer skill to identify potential impl issues. You forward the discovered issues to the implementation subagent and make it fix the issues. The implementation agent re-run tests after the fix. You repeat this feedback between the implementation agent and the code reviwer agent until the code reviewer reports that the code is clean. Finally, the implementation agent creates a git commit for the changes made. If the commit already exists and the fix is a direct response to review feedback, instruct the implementation agent to amend or fixup the original commit rather than creating a new standalone commit.

Then, you can move on to the next step. You start a new implementation subagent and repeat this cycle until the end of the steps.
