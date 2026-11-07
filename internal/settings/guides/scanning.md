The built-in scanner runs on every publish and import. An external scanner runs after it, and its findings join the same report. The tool must be installed on the server and must print SARIF to standard output.

- gitleaks finds secrets: `gitleaks dir {dir} --report-format sarif --report-path /dev/stdout --exit-code 0`
- Cisco skill-scanner finds risky agent instructions: `skill-scanner scan {dir} --format sarif`, installed with `pip install cisco-ai-skill-scanner`

The test runs the command on a sample skill and parses its output.
