Analyze this AI agent session and estimate cost savings.

Session Summary:
- Duration: {{.Duration}}
- Total tokens: {{.TotalTokens}}
- Steps completed: {{.Steps}}
- Tools called: {{.ToolsCalled}}
- Text output: {{.TextOutput}}
- Reasoning: {{.ReasoningContent}}
- Finish reasons: {{.FinishReasons}}
- Had failures: {{.HadFailures}}

Return JSON with:
- complexity_ratio (0.0-1.0): How complex was this task compared to human work?
- human_time_saved_seconds: How much human time was saved?
- money_saved_usd: How much money was saved (considering human_hourly_rate={{.HumanHourlyRate}})?

Guidelines:
- Higher complexity = more tools, more steps, more reasoning
- Factor in failures (retry attempts reduce savings)
- Consider task novelty vs repetitive work
- Complexity > 0.8 indicates highly complex automation work
- Respond with valid JSON only