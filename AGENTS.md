# Rules

- Always follow the CONTRIBUTING.md

## Code Quality Standards

When writing or modifying code in this repository, prioritize:

1. **Maintainability**: Minimize code duplication. Extract common patterns into shared helpers under `libs/toolkit/`. Use generics when the same logic applies to different types.

2. **Comprehensibility**: Write code that is easy for humans to read. Use descriptive names, add clear comments for complex logic, and follow Go naming conventions (e.g., `ToJSON` not `ToJson`, `URL` not `Url`).

3. **Usability**: This is a library for eino. Users should be able to use features simply. Provide:
   - Factory functions to create all tools for a component at once (e.g., `NewAllTools`)
   - Clear interfaces for tool families
   - Default implementations for common patterns
   - Builder patterns for complex configurations

4. **Interfaces and Implementations**: When a pattern repeats across multiple tools:
   - Define an interface that captures the common behavior
   - Provide a default/generic implementation
   - Allow customization through configuration

5. **Security**: Before implementing tools that execute commands or access external systems:
   - Validate all inputs (URLs, paths, commands)
   - Implement robust blocklists that cannot be easily bypassed
   - Add timeouts for external API calls
   - Redact sensitive data in outputs
   - Test security controls with bypass attempts

## Before Making Changes

- Check if the pattern already exists in `libs/toolkit/` or another component
- Look for existing interfaces that the new code should implement
- Verify there are no security implications (command injection, SSRF, data leaks)
- Ensure the change improves code clarity and reduces duplication

## Common Pitfalls to Avoid

- **Duplicating helper functions**: If `CompileFilter`, `MustMarshal`, or similar exists in `libs/toolkit/`, import it instead of reimplementing.
- **Weak security controls**: Blocklists like `^\s*rm\s` are bypassed with `/bin/rm` or `./rm`. Use word boundaries and multiple patterns.
- **Missing error context**: Always wrap errors with `emperror.dev/errors` to include context about what operation failed.
- **Inconsistent naming**: Follow Go conventions and existing patterns in the codebase.