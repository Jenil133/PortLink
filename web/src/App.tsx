import { useEffect, useMemo, useState } from "react";
import { lensClient } from "./net/client";
import { MapScene, webglAvailable } from "./scene/MapScene";
import type { EngineListeners, SceneEngine, SceneMode } from "./scene/engine";
import { setController, useLens } from "./state/store";
import { fleet } from "./state/vessels";
import { Console } from "./ui/console/Console";
import { StoryOverlay } from "./ui/story/StoryOverlay";

const params = new URLSearchParams(location.search);
const initialMode: SceneMode = location.pathname.startsWith("/console") || params.get("view") === "console" ? "console" : "story";

setController(lensClient);

export default function App() {
  const [engine, setEngine] = useState<SceneEngine | null>(null);
  // A deep link has no scroll travel until the user asks for the story.
  const [hasStory, setHasStory] = useState(initialMode === "story");
  const sceneMode = useLens((s) => s.sceneMode);

  const listeners = useMemo<EngineListeners>(
    () => ({
      onMode(mode) {
        useLens.getState().set({ sceneMode: mode });
        if (mode === "story") setHasStory(true);
      },
      onView(view) {
        useLens.getState().set({ view });
      },
      onSelect(mmsi) {
        lensClient.select(mmsi);
      },
      onUserPan() {
        const s = useLens.getState();
        if (!s.guide.pan) s.set({ guide: { ...s.guide, pan: true } });
      },
      onCameraChange() {
        lensClient.viewChanged();
      },
      onHover(mmsi, x, y) {
        const i = mmsi ? fleet.indexOf(mmsi) : -1;
        useLens.getState().set({ hover: i >= 0 ? { mmsi, x, y, name: fleet.names[i] || String(mmsi), aisType: fleet.aisType[i], sog: fleet.sog[i], cog: fleet.cog[i] } : null });
      },
    }),
    [],
  );

  useEffect(() => {
    lensClient.attach(engine);
    // Handles for the browser console, in development builds only.
    if (import.meta.env.DEV) Object.assign(window, { lens: engine, lensClient, fleet });
  }, [engine]);

  // The socket only runs in the console. The story works with no backend at all.
  useEffect(() => {
    if (!engine) return;
    if (sceneMode === "console") lensClient.start();
    else lensClient.stop();
  }, [engine, sceneMode]);

  useEffect(() => () => lensClient.stop(), []);

  if (!webglAvailable()) {
    return (
      <main style={{ padding: "var(--space-48)", maxWidth: 640 }}>
        <h1 className="d-title">PortLink Lens</h1>
        <p className="t-body-lg" style={{ marginTop: "var(--space-16)" }}>
          This browser cannot run WebGL, so the live map cannot be shown. The browser tells the server what it can see, and the server streams only that.
        </p>
      </main>
    );
  }

  return (
    <div style={{ height: hasStory ? "700vh" : "100vh" }}>
      <div style={{ position: "sticky", top: 0, height: "100vh" }}>
        <MapScene initialMode={initialMode} listeners={listeners} onEngine={setEngine} />
        {hasStory && <StoryOverlay engine={engine} />}
        <Console engine={engine} />
      </div>
    </div>
  );
}
