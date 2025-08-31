<!--
Copyright © 2025 Michael Shields

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
-->

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
