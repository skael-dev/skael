import type {
  User,
  Skill,
  SkillAnalytics,
  OverviewData,
  ActivationSummary,
  Version,
  ApiKeyInfo,
} from "@/api/types.gen";

// The signed-in user in tests is the instance owner: owner-only surfaces (the
// Team section) are otherwise unreachable. New accounts are members; admin is
// granted by the owner.
export const mockUser: User = {
  id: "user-001",
  email: "admin@test.com",
  name: "Admin User",
  role: "owner",
};

export type MockTeamUser = {
  id: string;
  email: string;
  name: string;
  role: string;
  created_at: string;
};

export const mockTeamUsers: MockTeamUser[] = [
  {
    id: "user-001",
    email: "admin@test.com",
    name: "Admin User",
    role: "owner",
    created_at: "2026-01-01T00:00:00Z",
  },
  {
    id: "user-002",
    email: "dana@test.com",
    name: "Dana Dev",
    role: "member",
    created_at: "2026-02-01T00:00:00Z",
  },
  {
    id: "user-003",
    email: "sam@test.com",
    name: "Sam Ops",
    role: "admin",
    created_at: "2026-03-01T00:00:00Z",
  },
];

export const mockSkills: Skill[] = [
  {
    id: "skill-001",
    name: "code-review",
    description: "Automated code review assistant",
    display_name: "Code Review",
    frontmatter: { tags: ["review", "quality"], version: 3 },
    author: "skael-team",
    license: "MIT",
    compatibility: "claude",
    tags: ["review", "quality"],
    spec_compliance: "full",
    latest_version: 3,
    reviewed_at: "2026-05-01T10:00:00Z",
    reviewed_by: "admin@test.com",
    created_at: "2026-01-15T08:00:00Z",
    updated_at: "2026-05-01T10:00:00Z",
  },
  {
    id: "skill-002",
    name: "test-writer",
    description: "Generates unit tests for existing code",
    display_name: "Test Writer",
    frontmatter: { tags: ["testing", "quality"], version: 1 },
    author: "",
    license: "",
    compatibility: "",
    tags: ["testing", "quality"],
    spec_compliance: "partial",
    latest_version: 1,
    reviewed_at: null,
    reviewed_by: "",
    created_at: "2026-03-10T09:00:00Z",
    updated_at: "2026-03-10T09:00:00Z",
  },
  {
    id: "skill-003",
    name: "doc-generator",
    description: "Generates documentation from code",
    display_name: "Doc Generator",
    frontmatter: { tags: ["docs"], version: 2 },
    author: "docs-team",
    license: "Apache-2.0",
    compatibility: "claude,cursor",
    tags: ["docs"],
    spec_compliance: "full",
    latest_version: 2,
    reviewed_at: "2026-04-20T14:30:00Z",
    reviewed_by: "admin@test.com",
    created_at: "2026-02-05T11:00:00Z",
    updated_at: "2026-04-20T14:30:00Z",
  },
];

export const mockSkillAnalytics: SkillAnalytics[] = [
  {
    name: "code-review",
    description: "Automated code review assistant",
    author: "skael-team",
    spec_compliance: "full",
    activations: 312,
    unique_devs: 18,
    last_triggered: "2026-05-24T16:45:00Z",
    latest_version: 3,
    reviewed_at: "2026-05-01T10:00:00Z",
    security_status: "clean",
    tags: ["review", "quality"],
    updated_at: "2026-05-01T10:00:00Z",
  },
  {
    name: "test-writer",
    description: "Generates unit tests for existing code",
    author: "",
    spec_compliance: "partial",
    activations: 156,
    unique_devs: 9,
    last_triggered: "2026-05-23T11:30:00Z",
    latest_version: 1,
    reviewed_at: null,
    security_status: "clean",
    tags: ["testing", "quality"],
    updated_at: "2026-03-10T09:00:00Z",
  },
  {
    name: "doc-generator",
    description: "Generates documentation from code",
    author: "docs-team",
    spec_compliance: "full",
    activations: 0,
    unique_devs: 0,
    last_triggered: null,
    latest_version: 2,
    reviewed_at: "2026-04-20T14:30:00Z",
    security_status: "clean",
    tags: ["docs"],
    updated_at: "2026-04-20T14:30:00Z",
  },
];

export const mockOverview: OverviewData = {
  total_skills: 3,
  active_skills: 2,
  total_activations: 468,
  unregistered_activations: 0,
  security: {
    clean: 3,
    warning: 0,
    critical: 0,
  },
};

export const mockActivations: ActivationSummary = {
  total_count: 312,
  unique_devs: 18,
  last_triggered: "2026-05-24T16:45:00Z",
  by_agent: {
    "claude-code": 210,
    cursor: 72,
    copilot: 30,
  },
  // by_agent and by_source are aggregated from the same rows (see
  // internal/analytics/event.go GetActivations) and must sum to the same
  // total_count. Cursor is the only transcript-scanning reporter, so its
  // count maps to transcript_scan; claude-code + copilot map to
  // tool_invocation: 210 + 30 = 240, plus cursor's 72 = 312.
  by_source: {
    tool_invocation: 240,
    transcript_scan: 72,
  },
};

export const mockVersions: Version[] = [
  {
    id: "ver-001",
    skill_id: "skill-001",
    version: 3,
    checksum: "abc123def456789a",
    changelog: "Improved review heuristics for TypeScript",
    frontmatter: { tags: ["review", "quality"], version: 3 },
    file_manifest: [
      { path: "SKILL.md", size: 4096 },
      { path: "examples/review.ts", size: 1024 },
    ],
    scan_result: {
      status: "clean",
      findings: [],
      summary: { critical: 0, high: 0, medium: 0, info: 0 },
    },
    published_by: "admin@test.com",
    created_at: "2026-05-01T10:00:00Z",
  },
  {
    id: "ver-002",
    skill_id: "skill-001",
    version: 2,
    checksum: "def456abc123789b",
    changelog: "Added support for Go files",
    frontmatter: { tags: ["review", "quality"], version: 2 },
    file_manifest: [
      { path: "SKILL.md", size: 3584 },
    ],
    scan_result: {
      status: "clean",
      findings: [],
      summary: { critical: 0, high: 0, medium: 0, info: 0 },
    },
    published_by: "admin@test.com",
    created_at: "2026-03-15T09:00:00Z",
  },
];

export const mockApiKeys: ApiKeyInfo[] = [
  {
    id: "key-001",
    name: "CI Pipeline",
    prefix: "sk_live_ci",
    created_at: "2026-01-01T00:00:00Z",
    last_used_at: "2026-05-24T12:00:00Z",
  },
  {
    id: "key-002",
    name: "Local Dev",
    prefix: "sk_live_dev",
    created_at: "2026-02-15T10:00:00Z",
    last_used_at: null,
  },
];

export const mockScanReport = {
  status: "clean",
  findings: [] as Array<{
    rule: string;
    severity: string;
    confidence: string;
    file: string;
    line: number;
    match: string;
    message: string;
  }>,
  summary: {
    critical: 0,
    high: 0,
    medium: 0,
    info: 0,
  },
};
