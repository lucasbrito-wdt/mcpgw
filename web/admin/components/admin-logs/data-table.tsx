// Plain data table for the admin screens that are not in v5 (clients, users, admin log): same surface as the v5 tables.
import type { ReactNode } from "react";

export interface DataTableColumn {
  label: string;
  /** Visually hidden header (action columns). */
  hidden?: boolean;
  className?: string;
}

export const CELL = "px-3 py-3 align-top text-[13px] text-fg";
export const ROW = "border-t border-line first:border-t-0 hover:bg-white/3";

/** Table in a rounded panel that scrolls sideways instead of squeezing; pass `<tr className={ROW}>` rows. */
export function DataTable({ caption, columns, children }: { caption: string; columns: readonly DataTableColumn[]; children: ReactNode }) {
  return (
    <div className="overflow-x-auto rounded-card border border-line bg-panel/85">
      <table className="w-full min-w-[40rem] border-collapse text-left">
        <caption className="sr-only">{caption}</caption>
        <thead className="border-b border-line-strong">
          <tr>
            {columns.map((column) => (
              <th key={column.label} scope="col" className={`px-3 py-2 text-xs font-medium text-fg-muted ${column.className ?? ""}`}>
                {column.hidden ? <span className="sr-only">{column.label}</span> : column.label}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>{children}</tbody>
      </table>
    </div>
  );
}
