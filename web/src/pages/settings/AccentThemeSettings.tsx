import { Check } from "lucide-react";

import { SettingsGroup } from "@/components/settings/SettingsGroup";
import { useAccentTheme } from "@/hooks/useAccentTheme";
import { ACCENT_THEMES } from "@/lib/accentThemes";
import { cn } from "@/lib/utils";

/**
 * Colour Theme: the same six themes as the TV and phone apps. The page re-tints
 * the moment one is chosen, so the swatches and the page itself preview it.
 * Kept in this browser only.
 */
export function AccentThemeSettings() {
  const [current, setCurrent] = useAccentTheme();
  return (
    <SettingsGroup
      title="Colour Theme"
      description="Tints buttons, selected items, outlines, cards and menus in this browser. Text colours stay as they are."
    >
      <div role="radiogroup" aria-label="Colour theme" className="flex flex-wrap gap-3">
        {ACCENT_THEMES.map((theme) => {
          const chosen = theme.id === current;
          return (
            <button
              key={theme.id}
              type="button"
              role="radio"
              aria-checked={chosen}
              onClick={() => setCurrent(theme.id)}
              className={cn(
                "bg-card flex min-w-[8.5rem] items-center gap-3 rounded-xl border px-3 py-2.5 text-left text-sm font-medium transition-colors",
                "hover:bg-surface-hover focus-visible:ring-ring focus-visible:ring-2 focus-visible:outline-none",
                chosen ? "border-ring" : "border-border",
              )}
            >
              <span
                aria-hidden="true"
                className="size-5 shrink-0 rounded-full ring-1 ring-white/20"
                style={{ backgroundColor: theme.swatch }}
              />
              <span className="flex-1">{theme.label}</span>
              <Check
                aria-hidden="true"
                className={cn("size-4 shrink-0", chosen ? "opacity-100" : "opacity-0")}
              />
            </button>
          );
        })}
      </div>
    </SettingsGroup>
  );
}
