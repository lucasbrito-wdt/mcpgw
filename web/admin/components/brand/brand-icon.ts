/** Renderable brand mark, 24x24 unless `viewBox` says otherwise. Color marks carry per-path fills; mono marks carry `hex` and are tinted by the renderer. */
export interface BrandIcon {
  paths: readonly { d: string; fill?: string }[];
  /** Brand color (no #) used to tint mono marks. */
  hex?: string;
  evenodd?: boolean;
  /** SVG viewBox when the source is not 24x24. */
  viewBox?: string;
}
