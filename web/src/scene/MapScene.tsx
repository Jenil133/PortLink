import { useEffect, useRef, useState } from "react";
import { Map as ReactMap, type MapRef } from "react-map-gl/maplibre";
import "maplibre-gl/dist/maplibre-gl.css";
import type { StyleSpecification } from "maplibre-gl";
import { loadBasemapStyle } from "./basemap";
import { SceneEngine, type EngineListeners, type SceneMode } from "./engine";
import { cameraAt, DEFAULT_CAMERA } from "./story/timeline";
import "./scene.css";

interface Props {
  initialMode: SceneMode;
  listeners?: EngineListeners;
  onEngine?: (engine: SceneEngine | null) => void;
}

export function webglAvailable(): boolean {
  try {
    return !!document.createElement("canvas").getContext("webgl2");
  } catch {
    return false;
  }
}

/** The single map canvas, plus the lens frame that scene 3 and the console's viewport ticks share. */
export function MapScene({ initialMode, listeners, onEngine }: Props) {
  const [style, setStyle] = useState<StyleSpecification | null>(null);
  const mapRef = useRef<MapRef>(null);
  const engineRef = useRef<SceneEngine | null>(null);
  const frame = useRef<HTMLDivElement>(null);
  const frameBorder = useRef<HTMLDivElement>(null);
  const frameLabel = useRef<HTMLDivElement>(null);

  useEffect(() => {
    let alive = true;
    loadBasemapStyle().then((s) => alive && setStyle(s));
    return () => {
      alive = false;
    };
  }, []);

  const handleLoad = async () => {
    const map = mapRef.current?.getMap();
    if (!map || engineRef.current) return;
    // Text layers build their atlas once, so the fonts must be ready first.
    await Promise.all([document.fonts.load('500 12px "Geist Mono Variable"'), document.fonts.load('500 12px "Geist Variable"')]);
    const engine = new SceneEngine(map, { frame: frame.current, frameBorder: frameBorder.current, frameLabel: frameLabel.current }, listeners ?? {}, initialMode);
    engineRef.current = engine;
    engine.start();
    onEngine?.(engine);
  };

  useEffect(
    () => () => {
      engineRef.current?.destroy();
      engineRef.current = null;
      onEngine?.(null);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  const first = initialMode === "story" ? cameraAt(0, innerWidth < 768, innerWidth / innerHeight) : DEFAULT_CAMERA;

  return (
    <div className="scene">
      {style && (
        <ReactMap
          ref={mapRef}
          mapStyle={style}
          initialViewState={{ longitude: first.lng, latitude: first.lat, zoom: first.zoom, pitch: first.pitch, bearing: first.bearing }}
          maxPitch={60}
          dragRotate={false}
          attributionControl={{ compact: true }}
          onLoad={handleLoad}
          style={{ width: "100%", height: "100%" }}
        />
      )}
      <div ref={frame} className="lens-frame" aria-hidden="true">
        <div ref={frameBorder} className="lens-frame__border" />
        <span className="lens-frame__tick lens-frame__tick--tl" />
        <span className="lens-frame__tick lens-frame__tick--tr" />
        <span className="lens-frame__tick lens-frame__tick--bl" />
        <span className="lens-frame__tick lens-frame__tick--br" />
        <div ref={frameLabel} className="lens-frame__label on-map">
          <span className="n-sm">0 of 0</span>
          <span className="t-label"> inside the lens</span>
        </div>
      </div>
    </div>
  );
}
