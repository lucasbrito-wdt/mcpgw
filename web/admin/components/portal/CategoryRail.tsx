"use client";

import { ChipRuler } from "../ui/chip-ruler";
import { useT } from "../../lib/i18n";
import { portalServers } from "../../lib/i18n/messages/portalServers";
import { categoryLabel } from "../../lib/portal-types";

/** Value of the "All" chip; categories are free slugs, so it cannot collide with a real one. */
export const ALL_CATEGORIES = "";

export interface CategoryRailProps {
  /** Every category seen so far (the list never shrinks while a category filter is active). */
  categories: readonly string[];
  value: string;
  onValueChange: (value: string) => void;
}

/** v5 category ruler fed by the categories of the loaded apps; picking one filters on the server. */
export function CategoryRail({ categories, value, onValueChange }: CategoryRailProps) {
  const t = useT(portalServers);
  const items = [
    { value: ALL_CATEGORIES, label: t("categoryAll") },
    ...categories.map((category) => ({ value: category, label: categoryLabel(category) })),
  ];
  return <ChipRuler label={t("categoryGroup")} items={items} value={value} onValueChange={onValueChange} />;
}
