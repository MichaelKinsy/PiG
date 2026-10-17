import "@earendil-works/pi-coding-agent";
import "@earendil-works/pi-tui";

export type * from "@earendil-works/pi-coding-agent";

/**
 * Data rendered by PiG's fixed native login template.
 *
 * The host validates grid dimensions, palette symbols, colors, and display
 * widths before replacing the current header.
 */
export interface PiGLoginDefinition {
  brand: string[];
  hero: string[];
  mascot: string[];
  palette: Record<string, string>;
  name: string;
  description: string;
  tagline: string;
}

/**
 * One sprite for PiG's /sprite catalogue: the 16-by-14 pig the startup header
 * draws and /sprite preview shows beside the wordmark, colored by the palette. The host validates the grids, palette symbols, colors and text.
 */
export interface PiGSpriteDefinition {
  id: string;
  name: string;
  tagline: string;
  mascot: string[];
  palette: Record<string, string>;
}

/** A Pi theme token a surface may override (PiG component kit, D107 §6). */
export type PiGViewThemeToken =
  | "accent"
  | "muted"
  | "dim"
  | "text"
  | "border"
  | "borderAccent"
  | "borderMuted"
  | "success"
  | "warning"
  | "error"
  | "selectedBg"
  | "customMessageBg";

/**
 * Token overrides for one surface, each a `#rrggbb` color. PiG renders the
 * surface with them in every mode, escaped in the theme's color mode; a faint
 * token stays faint. An unknown token or a malformed color is applied
 * nowhere, and a frontend rejects the surface's view.
 */
export type PiGViewTheme = Partial<Record<PiGViewThemeToken, `#${string}`>>;

/** One row of a {@link PiGViewList}: its primary text, a secondary text, and further table cells. */
export interface PiGViewListItem {
  label: string;
  detail?: string;
  columns?: string[];
}

/** Says a component's rows are a list: `items[i]` is row `i`. `selectedIndex` is the highlighted item, or -1. */
export interface PiGViewList {
  items: PiGViewListItem[];
  selectedIndex: number;
}

/**
 * What a component's rows show, for a frontend drawing them natively. PiG
 * sends it only while a frontend draws, and only for a component it cannot
 * read structure from (a custom `render`). The terminal always draws the rows.
 */
export interface PiGViewLines {
  /** The rows depict this image (base64 data). */
  image?: { data: string; mimeType: string };
  /** The rows show a position in a range. */
  progress?: { value: number; max: number };
  /** The rows are a list, one item per row; a list whose item count differs from the rows is rejected. */
  list?: PiGViewList;
}

declare module "@earendil-works/pi-coding-agent" {
  interface ExtensionUIContext {
    /** Set PiG's native login template, or reject an invalid definition. */
    setLogin(definition: PiGLoginDefinition): Promise<void>;
    /**
     * Add a sprite to PiG's /sprite catalogue, or reject an invalid definition.
     * Registering the same ID again replaces this extension's sprite; the sprite
     * leaves /sprite when the extension unloads.
     */
    registerSprite(definition: PiGSpriteDefinition): Promise<void>;
  }
}

declare module "@earendil-works/pi-tui" {
  interface Component {
    /** On a surface's root component: token overrides for that surface (PiG only). */
    viewTheme?: PiGViewTheme;
    /** Frontend-only annotations of this component's rows (PiG only). */
    viewLines?: PiGViewLines;
  }
}
