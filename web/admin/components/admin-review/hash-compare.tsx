// Ported from design/v5/src/components/admin-review/hash-compare.tsx on 2026-10-31.
// The divergent part is computed here (first differing character onward); "approved on" is omitted, the contract has no such date.
import { useT } from "../../lib/i18n";
import { adminReview } from "../../lib/i18n/messages/adminReview";
import { cn } from "../../lib/utils";

export interface HashCompareProps {
  approved: string | null;
  current: string;
}

function divergence(approved: string, current: string): number {
  let index = 0;
  while (index < approved.length && index < current.length && approved[index] === current[index]) index += 1;
  return index;
}

/**
 * Approved hash over current hash, same mono column so the divergent part lines up vertically.
 * The divergent part is marked in both; a new tool has no approved hash.
 */
export function HashCompare({ approved, current }: HashCompareProps) {
  const t = useT(adminReview);
  const from = approved ? divergence(approved, current) : current.length;
  return (
    <dl className="grid grid-cols-1 gap-x-6 gap-y-3 rounded-card border border-line bg-panel/70 p-4 sm:grid-cols-[auto_minmax(0,1fr)] sm:items-baseline">
      <dt className="text-[13px] text-fg-muted">{t("approvedHash")}</dt>
      <dd className="min-w-0 font-mono text-[13px] break-all">
        {approved ? <Hash value={approved} from={from} tone="approved" /> : <span className="font-sans text-fg-muted">{t("noApprovedHash")}</span>}
      </dd>
      <dt className="text-[13px] text-fg-muted">{t("currentHash")}</dt>
      <dd className="min-w-0 font-mono text-[13px] break-all">
        <Hash value={current} from={from} tone={approved ? "current" : "new"} />
      </dd>
      {approved ? (
        <p className="text-xs text-fg-muted sm:col-span-2">
          {t("divergeNote")}
        </p>
      ) : null}
    </dl>
  );
}

function Hash({ value, from, tone }: { value: string; from: number; tone: "approved" | "current" | "new" }) {
  const t = useT(adminReview);
  const same = value.slice(0, from);
  const rest = value.slice(from);
  return (
    <span className="text-fg">
      <span>{same}</span>
      {rest && tone !== "new" ? (
        <mark className={cn("rounded-[3px] px-0.5", tone === "approved" ? "bg-ok-wash text-ok ring-1 ring-ok/30 ring-inset" : "bg-cut-wash text-cut ring-1 ring-cut/35 ring-inset")}>
          <span className="sr-only">{t(tone === "approved" ? "srApproved" : "srDiverging")}</span>
          {rest}
        </mark>
      ) : (
        <span>{rest}</span>
      )}
    </span>
  );
}
