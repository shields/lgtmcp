# Context Gathering Prompt

You are analyzing code changes for a thorough review. Please examine this git diff and use the get_file_content tool to retrieve any additional context you need to understand the changes completely.

Changed files:

- {{.FilesList}}

Git diff to analyze:
{{.Diff}}

Use the get_file_content tool to examine any files you need more context about. Once you have gathered sufficient context, provide a brief analysis of what you've found that's relevant for the code review.

Focus on understanding:

1. How the changes fit into the overall codebase
2. Dependencies and imports that might be affected
3. Any security-sensitive operations
4. Performance implications
5. API or interface changes
