// Ported from design/v5/src/components/brand/brand-icons.ts on 2026-10-31.
import {
  siApachekafka,
  siAsana,
  siBrave,
  siConfluence,
  siDatadog,
  siDiscord,
  siGitlab,
  siGmail,
  siGooglechrome,
  siGooglecalendar,
  siGoogledrive,
  siGrafana,
  siHubspot,
  siJira,
  siLaravel,
  siLinear,
  siNetlify,
  siObsidian,
  siPagerduty,
  siPortainer,
  siPostgresql,
  siRedis,
  siSentry,
  siStripe,
  siSupabase,
  siWhatsapp,
} from "simple-icons";
import type { SimpleIcon } from "simple-icons";

import type { BrandIcon } from "./brand-icon";
import {
  lobeClaude,
  lobeClaudecode,
  lobeCloudflare,
  lobeCodex,
  lobeCursor,
  lobeFigma,
  lobeFirecrawl,
  lobeGithub,
  lobeMeta,
  lobeNotion,
  lobeVercel,
} from "./lobehub-icons";
import {
  projectCrawl4ai,
  projectDokploy,
  projectEasypanel,
  projectForgeboard,
  projectObscura,
  projectRea,
  projectUiLayouts,
  projectVgpu,
  projectVscode,
} from "./project-icons";
import { resolveBrandKey } from "./resolve-brand";

/**
 * Secondary registry: simple-icons (CC0) marks for slugs LobeHub does not have.
 * Neither set has slack, playwright or context7: those fall back to the hashed monogram, as does the placeholder
 * slug acme. Brands missing from both sets but registered here as official project logos (PROJECT_ICONS):
 * dokploy, easypanel, crawl4ai, obscura, rea, ui_layouts, vgpu, forgeboard, vscode.
 */
const SIMPLE_ICONS: Readonly<Record<string, SimpleIcon>> = {
  linear: siLinear,
  jira: siJira,
  sentry: siSentry,
  "postgres-ro": siPostgresql,
  postgres: siPostgresql,
  chrome: siGooglechrome,
  kafka: siApachekafka,
  telescope: siLaravel,
  netlify: siNetlify,
  portainer: siPortainer,
  whatsapp: siWhatsapp,
  gitlab: siGitlab,
  gmail: siGmail,
  grafana: siGrafana,
  "brave-search": siBrave,
  "google-drive": siGoogledrive,
  "google-calendar": siGooglecalendar,
  stripe: siStripe,
  hubspot: siHubspot,
  supabase: siSupabase,
  redis: siRedis,
  datadog: siDatadog,
  pagerduty: siPagerduty,
  confluence: siConfluence,
  asana: siAsana,
  discord: siDiscord,
  obsidian: siObsidian,
};

/** LobeHub Icons (MIT) win over simple-icons: richer color marks, and the set the gateway standardizes on. */
const LOBEHUB_ICONS: Readonly<Record<string, BrandIcon>> = {
  github: lobeGithub,
  cloudflare: lobeCloudflare,
  firecrawl: lobeFirecrawl,
  vercel: lobeVercel,
  meta: lobeMeta,
  notion: lobeNotion,
  cursor: lobeCursor,
  figma: lobeFigma,
  "claude-code": lobeClaudecode,
  "claude-desktop": lobeClaude,
  codex: lobeCodex,
};

/** Official project logos (third source) for brands neither LobeHub nor simple-icons carries. */
const PROJECT_ICONS: Readonly<Record<string, BrandIcon>> = {
  dokploy: projectDokploy,
  easypanel: projectEasypanel,
  crawl4ai: projectCrawl4ai,
  obscura: projectObscura,
  rea: projectRea,
  ui_layouts: projectUiLayouts,
  vgpu: projectVgpu,
  forgeboard: projectForgeboard,
  vscode: projectVscode,
};

const hasKey = (key: string) => key in LOBEHUB_ICONS || key in SIMPLE_ICONS || key in PROJECT_ICONS;

function toBrandIcon(key: string): BrandIcon {
  const lobe = LOBEHUB_ICONS[key];
  if (lobe) return lobe;
  const simple = SIMPLE_ICONS[key];
  if (!simple) return PROJECT_ICONS[key];
  return { paths: [{ d: simple.path }], hex: simple.hex };
}

/** Brand mark for a server slug (exact, then first segment), optionally its URL (host keyword). */
export function getBrandIcon(slug: string, url?: string): BrandIcon | undefined {
  const key = resolveBrandKey(slug, url, hasKey);
  return key ? toBrandIcon(key) : undefined;
}

export function hasBrandIcon(slug: string, url?: string): boolean {
  return resolveBrandKey(slug, url, hasKey) !== undefined;
}
