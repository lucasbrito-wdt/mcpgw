// Replaces design/v5/src/components/admin-review/definition-diff.tsx. The API returns the current (pending) definition, its hash,
// the approved hash and which parts changed, but not the approved definition text, so this shows hashes plus the current text
// of each part, flagged when it changed. Prompt-injection marks are omitted (no contract data, plan gap G4).
import { formatDefinition, type ToolDetail } from "../../lib/admin-types";
import { useT, type Translator } from "../../lib/i18n";
import { adminReview } from "../../lib/i18n/messages/adminReview";
import { HashCompare } from "../admin-review/hash-compare";
import { Badge } from "../ui/badge";

type T = Translator<typeof adminReview.en>;

interface Part {
  title: string;
  /** Keys of `pending_changes` that flag this part. */
  keys: string[];
  text: string;
}

const CHANGE_KEY = {
  description: "partDescription",
  schema: "partInput",
  input_schema: "partInput",
  output_schema: "partOutput",
  annotations: "partAnnotations",
} as const;

const changeLabel = (t: T, change: string) => (Object.hasOwn(CHANGE_KEY, change) ? t(CHANGE_KEY[change as keyof typeof CHANGE_KEY]) : change);

function parts(tool: ToolDetail, t: T): Part[] {
  return [
    { title: t("partDescription"), keys: ["description"], text: tool.description },
    { title: t("partInput"), keys: ["schema", "input_schema"], text: formatDefinition(tool.input_schema) },
    { title: t("partOutput"), keys: ["output_schema"], text: formatDefinition(tool.output_schema) },
    { title: t("partAnnotations"), keys: ["annotations"], text: formatDefinition(tool.annotations) },
  ].filter((part) => part.text.trim() !== "" && part.text.trim() !== "{}");
}

/** Hash comparison and the current definition of a pending tool. Every definition text is untrusted: text nodes only. */
export function ToolDiff({ tool }: { tool: ToolDetail }) {
  const t = useT(adminReview);
  const isNew = !tool.approved_hash;
  const changes = tool.pending_changes ?? [];
  return (
    <div className="flex flex-col gap-5">
      <HashCompare approved={tool.approved_hash || null} current={tool.hash} />
      {changes.length > 0 ? (
        <p className="flex flex-wrap items-center gap-2 text-[13px] text-fg-muted">
          {t("changedUpstream")}
          {changes.map((change) => (
            <Badge key={change} tone="cut">
              {changeLabel(t, change)}
            </Badge>
          ))}
        </p>
      ) : null}
      <section className="flex flex-col gap-3">
        <h3 className="text-[13px] font-semibold text-fg">{t(isNew ? "definition" : "definitionPending")}</h3>
        {parts(tool, t).map((part) => (
          <div key={part.title} className="overflow-hidden rounded-card border border-line bg-ink/70">
            <p className="flex items-center gap-2 border-b border-line px-4 py-2 text-xs font-medium text-fg-muted">
              {part.title}
              {part.keys.some((key) => changes.includes(key)) ? <Badge tone="cut">{t("changedBadge")}</Badge> : null}
            </p>
            <pre className="max-h-96 overflow-auto px-4 py-3 font-mono text-xs leading-6 whitespace-pre-wrap break-words text-fg-muted">{part.text}</pre>
          </div>
        ))}
      </section>
    </div>
  );
}
