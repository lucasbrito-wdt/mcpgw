// Ported from design/v5/src/components/admin-servers/wizard-state.ts on 2026-10-31.
// Pure state and validation of the add-server wizard. Nothing here touches React.
import type { AddServerForm, StdioEnvRow } from "../../lib/admin-types";
import { connectionEnvNamesError, parseEnvNames } from "../../lib/connection-env";
import { getLocale, translate, type Vars } from "../../lib/i18n";
import { serversWizard } from "../../lib/i18n/messages/serversWizard";

export type WizardTransport = "auto" | "stdio";

/** Step 1: which server and where it runs. */
export interface ServerDraft {
  slug: string;
  transport: WizardTransport;
  url: string;
  dockerImage: string;
  /** Docker only: optional command run in the image. */
  command: string;
  /** Docker only: whether the container can reach the network. */
  network: boolean;
  /** Fixed env of the process; values are secrets. */
  env: StdioEnvRow[];
  category: string;
}

/** Step 2, Header: how the header is built, plus the optional shared value (write-only). */
export interface HeaderDraft {
  headerName: string;
  prefix: string;
  format: string;
  helpUrl: string;
  sharedToken: string;
  /** stdio only: env var each user's token is injected in; empty means the server has no per-user credential. */
  credentialEnv: string;
  /** stdio with a credential: variables each connection informs itself, comma or space separated. */
  connectionEnv: string;
}

/** Step 2, OAuth: the gateway registers its own client (DCR) or the admin brings one. */
export interface OAuthDraft {
  clientMode: "dcr" | "own";
  clientId: string;
  clientSecret: string;
}

export const EMPTY_SERVER: ServerDraft = { slug: "", transport: "auto", url: "", dockerImage: "", command: "", network: false, env: [], category: "" };
export const EMPTY_HEADER: HeaderDraft = { headerName: "Authorization", prefix: "", format: "", helpUrl: "", sharedToken: "", credentialEnv: "", connectionEnv: "" };
export const EMPTY_OAUTH: OAuthDraft = { clientMode: "dcr", clientId: "", clientSecret: "" };

const copy = (key: keyof typeof serversWizard.en, vars?: Vars): string => translate(getLocale(), serversWizard, key, vars);

export const slugHint = (): string => copy("slugHint");
const SLUG_PATTERN = /^[a-z][a-z0-9_]{0,62}$/;
const CATEGORY_PATTERN = /^[a-z][a-z0-9_-]{0,31}$/;
const ENV_NAME_PATTERN = /^[A-Z_][A-Z0-9_]{0,63}$/;
const HEADER_NAME_PATTERN = /^[A-Za-z0-9-]{1,100}$/;

export const hasErrors = (errors: object): boolean => Object.values(errors).some(Boolean);
export const lastFour = (value: string): string => value.trim().slice(-4);

/** True when the pasted value already starts with the configured prefix (422 prefix_already_in_value). */
export function startsWithPrefix(value: string, prefix: string): boolean {
  const wanted = prefix.trim().toLowerCase();
  if (!wanted) return false;
  return value.trimStart().toLowerCase().startsWith(`${wanted} `);
}

export function stripPrefix(value: string, prefix: string): string {
  return startsWithPrefix(value, prefix) ? value.trimStart().slice(prefix.trim().length).trimStart() : value;
}

function parseUrl(raw: string): URL | null {
  try {
    return new URL(raw.trim());
  } catch {
    return null;
  }
}

/** The auth_kind a stdio server gets: a per-user credential makes it `header`, otherwise secrets live in the fixed env. */
export const stdioAuthKind = (credentialEnv: string): AddServerForm["auth_kind"] => (credentialEnv.trim() ? "header" : "none");

const filledEnv = (env: StdioEnvRow[]): StdioEnvRow[] => env.filter((row) => row.name.trim() !== "");

function envError(env: StdioEnvRow[]): string | undefined {
  const names = filledEnv(env).map((row) => row.name.trim());
  const bad = names.find((name) => !ENV_NAME_PATTERN.test(name));
  if (bad) return copy("envInvalid", { name: bad });
  const dup = names.find((name, i) => names.indexOf(name) !== i);
  return dup ? copy("envDuplicate", { name: dup }) : undefined;
}

export interface ServerDraftErrors {
  slug?: string;
  url?: string;
  dockerImage?: string;
  env?: string;
  category?: string;
}

/** Client-side mirror of the server checks, so the common mistakes show before the request. */
export function serverDraftErrors(draft: ServerDraft, authKind: AddServerForm["auth_kind"]): ServerDraftErrors {
  const errors: ServerDraftErrors = {};
  if (!SLUG_PATTERN.test(draft.slug)) errors.slug = slugHint();
  if (draft.category.trim() && !CATEGORY_PATTERN.test(draft.category.trim())) {
    errors.category = copy("categoryInvalid");
  }
  if (draft.transport === "stdio") {
    if (!draft.dockerImage.trim()) errors.dockerImage = copy("dockerImageRequired");
    errors.env = envError(draft.env);
    return errors;
  }
  const url = parseUrl(draft.url);
  if (!url || (url.protocol !== "https:" && url.protocol !== "http:")) errors.url = copy("urlRequired");
  else if (authKind === "oauth" && url.protocol !== "https:") errors.url = copy("urlOauth");
  return errors;
}

export interface HeaderDraftErrors {
  credentialEnv?: string;
  connectionEnv?: string;
  headerName?: string;
  prefix?: string;
  format?: string;
  helpUrl?: string;
  sharedToken?: { code: "prefix_already_in_value"; message: string };
}

/** `fixedEnv` is the stdio env of step 1: the credential variable must not collide with it. */
export function headerDraftErrors(draft: HeaderDraft, fixedEnv: StdioEnvRow[] = []): HeaderDraftErrors {
  const errors: HeaderDraftErrors = {};
  const credentialEnv = draft.credentialEnv.trim();
  if (credentialEnv && !ENV_NAME_PATTERN.test(credentialEnv)) errors.credentialEnv = copy("credEnvFormat");
  else if (credentialEnv && filledEnv(fixedEnv).some((row) => row.name.trim() === credentialEnv)) errors.credentialEnv = copy("credEnvCollide", { name: credentialEnv });
  errors.connectionEnv = connectionEnvNamesError(draft.connectionEnv, credentialEnv);
  const name = draft.headerName.trim();
  if (!name) errors.headerName = copy("headerNameRequired");
  else if (!HEADER_NAME_PATTERN.test(name)) errors.headerName = copy("headerNameInvalid");
  if (draft.helpUrl.trim() && !draft.helpUrl.trim().startsWith("https://")) errors.helpUrl = copy("helpUrlHttps");
  if (startsWithPrefix(draft.sharedToken, draft.prefix)) {
    errors.sharedToken = { code: "prefix_already_in_value", message: copy("sharedTokenPrefix", { prefix: draft.prefix.trim() }) };
  }
  return errors;
}

export interface OAuthDraftErrors {
  clientId?: string;
  clientSecret?: string;
}

export function oauthDraftErrors(draft: OAuthDraft): OAuthDraftErrors {
  const errors: OAuthDraftErrors = {};
  if (draft.clientMode !== "own") return errors;
  if (!draft.clientId.trim()) errors.clientId = copy("oauthClientIdRequired");
  if (!draft.clientSecret.trim()) errors.clientSecret = copy("oauthSecretRequired");
  return errors;
}

/** Maps the three drafts to the flat form `buildAddServerBody` takes. */
export function toAddServerForm(server: ServerDraft, authKind: AddServerForm["auth_kind"], header: HeaderDraft): AddServerForm {
  const docker = server.transport === "stdio";
  return {
    slug: server.slug,
    url: server.transport === "stdio" ? undefined : server.url,
    transport: server.transport,
    docker_image: server.dockerImage,
    stdio_command: docker ? server.command : undefined,
    stdio_network: docker ? server.network : undefined,
    stdio_env: docker ? server.env : undefined,
    credential_env: docker ? header.credentialEnv : undefined,
    connection_env: docker ? parseEnvNames(header.connectionEnv) : undefined,
    category: server.category,
    auth_kind: authKind,
    header_name: header.headerName,
    header_prefix: header.prefix,
    header_format_hint: header.format,
    header_help_url: header.helpUrl,
  };
}

type RemoteFields = Record<string, string> | undefined;

/** Server `fields` (API field names) that belong to step 1. */
export function remoteServerErrors(fields: RemoteFields): ServerDraftErrors {
  return {
    slug: fields?.slug,
    url: fields?.url,
    category: fields?.category,
    dockerImage: fields?.stdio_launcher ?? fields?.stdio,
    env: fields?.stdio_env,
  };
}

/** Server `fields` that belong to the header form of step 2. */
export function remoteHeaderErrors(fields: RemoteFields): HeaderDraftErrors {
  return {
    credentialEnv: fields?.credential_env ?? fields?.auth_kind ?? fields?.header,
    connectionEnv: fields?.connection_env,
    headerName: fields?.name ?? fields?.header_name,
    prefix: fields?.header_prefix,
    format: fields?.header_format_hint,
    helpUrl: fields?.header_help_url,
  };
}
