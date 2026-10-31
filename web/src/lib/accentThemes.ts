/** The Spoel accent themes, matching the TV and phone apps. Generated alongside styles/accent-themes.css. */
export const ACCENT_THEMES = [
  { id: "default", label: "Default", swatch: "#e8e8ec" },
  { id: "purple", label: "Spoel Purple", swatch: "#7C3AED" },
  { id: "blue", label: "Light Blue", swatch: "#7DD3FC" },
  { id: "orange", label: "Orange", swatch: "#FB923C" },
  { id: "green", label: "Green", swatch: "#4ADE80" },
  { id: "red", label: "Red", swatch: "#DC2626" },
] as const;

export type AccentThemeId = (typeof ACCENT_THEMES)[number]["id"];
