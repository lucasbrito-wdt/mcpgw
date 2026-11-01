/** Static export embedded in the Go binary (web/admin/embed.go). No server features. */
const config = {
  output: 'export',
  trailingSlash: true,
  images: { unoptimized: true },
  poweredByHeader: false,
  reactStrictMode: true,
  agentRules: false, // keeps next dev from writing AGENTS.md / CLAUDE.md into web/admin
};
export default config;
