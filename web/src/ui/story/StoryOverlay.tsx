// Story copy, progress rail and the pearl handoff. Everything is set imperatively from progress p, so
// React never re-renders while the user scrolls.
import { useEffect, useRef } from "react";
import type { SceneEngine } from "../../scene/engine";
import { lerp, range, smoothstep } from "../../scene/math";
import { sceneIndex } from "../../scene/story/timeline";
import "./story.css";

const SCENES = [
  { name: "Night coast", start: 0 },
  { name: "Everything at once", start: 0.16 },
  { name: "The lens", start: 0.34 },
  { name: "Time", start: 0.54 },
  { name: "One vessel", start: 0.7 },
  { name: "Your screen is the lens", start: 0.88 },
];

interface Fade {
  id: string;
  in: [number, number];
  out?: [number, number];
}

const FADES: Fade[] = [
  // Visible from the first frame: nobody should land on a page with no words on it.
  { id: "s1-title", in: [-2, -1], out: [0.13, 0.16] },
  { id: "s1-text", in: [-2, -1], out: [0.13, 0.16] },
  { id: "s1-cue", in: [-2, -1], out: [0.005, 0.03] },
  { id: "s2-copy", in: [0.18, 0.22], out: [0.31, 0.34] },
  { id: "s2-count", in: [0.24, 0.28], out: [0.31, 0.34] },
  { id: "s3-copy", in: [0.36, 0.4], out: [0.51, 0.54] },
  { id: "s4-copy", in: [0.56, 0.6], out: [0.67, 0.7] },
  { id: "s4-offset", in: [0.58, 0.6], out: [0.67, 0.7] },
  { id: "s5-copy", in: [0.72, 0.76], out: [0.86, 0.88] },
];

const INSTRUMENTS = [
  { label: "SOG", value: "13.6", unit: "kn", angle: -130 },
  { label: "RPM", value: "1,642", unit: "", angle: -50 },
  { label: "fuel rate", value: "187.3", unit: "L/h", angle: 10 },
  { label: "engine temp", value: "84.1", unit: "C", angle: 60 },
  { label: "fuel level", value: "61.8", unit: "%", angle: 178 },
];
const LEADER_R = 200;

const offsetFor = (p: number) => {
  const totalMin = Math.round(range(p, 0.58, 0.68) * 360);
  return `-${Math.floor(totalMin / 60)}h ${String(totalMin % 60).padStart(2, "0")}m`;
};

const TEAL: [number, number, number] = [10, 58, 71];
const PEARL: [number, number, number] = [242, 240, 233];

export function StoryOverlay({ engine }: { engine: SceneEngine | null }) {
  const root = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const el = root.current;
    if (!el || !engine) return;
    const reduced = matchMedia("(prefers-reduced-motion: reduce)").matches;

    const q = <T extends HTMLElement = HTMLElement>(id: string) => el.querySelector<T>(`[data-id="${id}"]`)!;
    const fades = FADES.map((f) => ({ ...f, el: q(f.id) }));
    const offset = q("s4-value");
    const name = q("s5-name");
    const instruments = INSTRUMENTS.map((_, i) => q(`inst-${i}`));
    const leaders = INSTRUMENTS.map((_, i) => q<HTMLElement>(`leader-${i}`) as unknown as SVGLineElement);
    const leaderSvg = q("leaders");
    const pearl = q("pearl");
    const line1 = q("line1");
    const line2 = q("line2");
    const rail = q("rail");
    const ticks = SCENES.map((_, i) => q(`tick-${i}`));
    const railName = q("rail-name");
    const skip = q("skip");
    const corner = q("corner");

    let lastScene = -1;
    let nameTimer = 0;

    const setFade = (e: HTMLElement, o: number, y: number) => {
      e.style.opacity = String(o);
      e.style.visibility = o > 0 ? "visible" : "hidden";
      if (!reduced && e.dataset.id !== "s1-cue") e.style.transform = `translateY(${y.toFixed(1)}px)`;
    };

    const update = (p: number) => {
      for (const f of fades) {
        const inT = smoothstep(range(p, f.in[0], f.in[1]));
        const outT = f.out ? smoothstep(range(p, f.out[0], f.out[1])) : 0;
        setFade(f.el, inT * (1 - outT), lerp(24, 0, inT) - 24 * outT);
      }

      offset.textContent = offsetFor(p);

      // Scene 5: instruments assemble around the hull, with leaders re-aimed as the camera orbits.
      const s5 = range(p, 0.76, 0.88);
      if (s5 > 0 && p < 0.88) {
        const c = engine.storyVesselScreen();
        const exit = 1 - smoothstep(range(p, 0.86, 0.88));
        const narrow = innerWidth < 768;
        const r = narrow ? 120 : LEADER_R;
        leaderSvg.style.visibility = "visible";
        INSTRUMENTS.forEach((inst, i) => {
          const start = 0.79 + i * 0.01;
          const t = smoothstep(range(p, start, start + 0.02)) * exit;
          const a = (inst.angle * Math.PI) / 180;
          const x = c.x + Math.cos(a) * r;
          const y = c.y + Math.sin(a) * r;
          const box = instruments[i];
          box.style.opacity = String(t);
          box.style.visibility = t > 0 ? "visible" : "hidden";
          box.style.transform = `translate(${x.toFixed(1)}px, ${y.toFixed(1)}px) translate(${Math.cos(a) < 0 ? "-100%" : "0"}, -50%) translateY(${reduced ? 0 : ((1 - t) * 12).toFixed(1)}px)`;
          // Leader stops 32 px short of the hull and draws outward.
          const x0 = c.x + Math.cos(a) * 32;
          const y0 = c.y + Math.sin(a) * 32;
          const line = leaders[i];
          line.setAttribute("x1", x0.toFixed(1));
          line.setAttribute("y1", y0.toFixed(1));
          line.setAttribute("x2", lerp(x0, x, t).toFixed(1));
          line.setAttribute("y2", lerp(y0, y, t).toFixed(1));
          line.style.opacity = String(t);
        });
      } else {
        leaderSvg.style.visibility = "hidden";
        for (const box of instruments) box.style.visibility = "hidden";
      }
      const nameT = smoothstep(range(p, 0.76, 0.79)) * (1 - smoothstep(range(p, 0.86, 0.88)));
      name.style.opacity = String(nameT);
      name.style.visibility = nameT > 0 ? "visible" : "hidden";

      // Scene 6 handoff.
      const rise = smoothstep(range(p, 0.88, 0.905));
      const fade = 1 - range(p, 0.94, 0.96);
      pearl.style.visibility = rise > 0 && fade > 0 ? "visible" : "hidden";
      pearl.style.opacity = String(reduced ? rise * fade : fade);
      if (!reduced) pearl.style.transform = `translateY(${((1 - rise) * 100).toFixed(2)}%)`;
      const swap = range(p, 0.925, 0.94);
      line1.style.opacity = String(1 - swap);
      line1.style.visibility = swap < 1 ? "visible" : "hidden";
      pearl.style.pointerEvents = rise === 1 && swap === 0 ? "auto" : "none";

      const line2T = swap * (1 - range(p, 0.97, 0.98));
      line2.style.opacity = String(line2T);
      line2.style.visibility = line2T > 0 ? "visible" : "hidden";
      const c = range(p, 0.94, 0.96);
      line2.style.color = `rgb(${lerp(TEAL[0], PEARL[0], c)}, ${lerp(TEAL[1], PEARL[1], c)}, ${lerp(TEAL[2], PEARL[2], c)})`;

      // Rail and always present chrome.
      const chrome = 1 - range(p, 0.96, 1);
      rail.style.opacity = String(chrome);
      rail.style.visibility = chrome > 0 ? "visible" : "hidden";
      // The skip link would sit on pearl with no contrast during the handoff, where a real button takes over.
      const skipO = 1 - range(p, 0.87, 0.89);
      skip.style.opacity = String(skipO);
      skip.style.visibility = skipO > 0 ? "visible" : "hidden";
      corner.style.opacity = String(skipO);

      const scene = sceneIndex(p);
      if (scene !== lastScene) {
        ticks[Math.max(0, lastScene)]?.classList.remove("is-active");
        ticks[scene].classList.add("is-active");
        ticks[scene].setAttribute("aria-current", "step");
        if (lastScene >= 0) ticks[lastScene].removeAttribute("aria-current");
        railName.textContent = SCENES[scene].name;
        railName.style.top = `${(scene / (SCENES.length - 1)) * 100}%`;
        railName.classList.add("is-shown");
        clearTimeout(nameTimer);
        nameTimer = window.setTimeout(() => railName.classList.remove("is-shown"), 1500);
        lastScene = scene;
      }
    };

    const unsub = engine.subscribeStory(update);
    return () => {
      unsub();
      clearTimeout(nameTimer);
    };
  }, [engine]);

  const jump = (top: number) => scrollTo({ top, behavior: matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth" });
  const toConsole = () => jump(6 * innerHeight);

  return (
    <div ref={root} className="story">
      <div className="story__block" data-id="s1-title" style={{ top: 120 }}>
        <h1 className="d-hero on-map">PortLink Lens</h1>
      </div>
      <div className="story__block" data-id="s1-text" style={{ top: 212 }}>
        <p className="t-body-lg on-map">The browser tells the server what it can see. The server streams only that.</p>
      </div>

      <p className="t-label story__cue on-map" data-id="s1-cue">
        Scroll
      </p>

      <div className="story__block" data-id="s2-copy" style={{ top: 120 }}>
        <h2 className="d-title on-map">Everything at once</h2>
        <p className="t-body-lg on-map story__text">The usual approach downloads every vessel, every field, every second, then filters in the browser. The cost grows with the fleet, not with what you can see.</p>
      </div>
      <div className="story__block" data-id="s2-count" style={{ top: 316 }}>
        <p className="n-hero on-map">2,000</p>
        <p className="t-label on-map story__muted">vessels in the fixture</p>
      </div>

      <div className="story__block story__block--narrow" data-id="s3-copy" style={{ top: 100 }}>
        <h2 className="d-title on-map">The lens</h2>
        <p className="t-body-lg on-map story__text">The browser sends its bounding box, its zoom and its time cursor. The server streams only the vessels inside, at the resolution that zoom needs.</p>
      </div>

      <div className="story__block" data-id="s4-copy" style={{ top: 100 }}>
        <h2 className="d-title on-map">Time</h2>
        <p className="t-body-lg on-map story__text">History is the same request with a time cursor. Move it and the server sends the past, in one minute buckets here and one second buckets up close.</p>
      </div>
      <div className="story__block" data-id="s4-offset" style={{ top: 296 }}>
        <p className="t-label on-map story__muted">cursor</p>
        <p className="n-hero on-map" data-id="s4-value">
          -0h 00m
        </p>
        <p className="t-label on-map story__muted">of 6 hours, one minute buckets</p>
      </div>

      <div className="story__block" data-id="s5-copy" style={{ top: 100 }}>
        <h2 className="d-title on-map">One vessel</h2>
        <p className="t-body-lg on-map story__text">Select a vessel and five instruments arrive with it, each a six hour series reduced to 600 points.</p>
      </div>
      <div className="story__name" data-id="s5-name">
        <p className="d-head on-map">SYN-0417</p>
        <p className="on-map">
          <span className="n-xs">900000417</span> <span className="t-label">cargo</span>
        </p>
      </div>

      <svg className="story__leaders" data-id="leaders" aria-hidden="true">
        {INSTRUMENTS.map((_, i) => (
          <line key={i} data-id={`leader-${i}`} stroke="#F2F0E9" strokeWidth="1" />
        ))}
      </svg>
      {INSTRUMENTS.map((inst, i) => (
        <div key={inst.label} className="story__instrument on-map" data-id={`inst-${i}`}>
          <p className="t-label story__muted">{inst.label}</p>
          <p>
            <span className="n-md">{inst.value}</span> <span className="n-unit story__muted">{inst.unit}</span>
          </p>
        </div>
      ))}

      <section className="story__pearl on-card" data-id="pearl">
        <div className="story__pearl-main" data-id="line1">
          <h2 className="d-title story__statement">Front end performance at this scale is a backend contract.</h2>
          <p className="t-body-lg story__pearl-text">PortLink Lens makes that contract visible: what the browser asked for, what the server sent, and what it cost, measured live.</p>
        </div>
        <div className="story__band">
          <button className="story__cta" onClick={toConsole}>
            Open live console
          </button>
          <span className="t-body">or keep scrolling</span>
          <span className="t-label story__band-note">one canvas, no reload</span>
        </div>
      </section>
      <p className="d-hero story__line2" data-id="line2">
        Your screen is the lens.
      </p>

      <button className="story__skip" data-id="skip" onClick={toConsole}>
        Open live console
      </button>
      <p className="t-label story__corner on-map" data-id="corner">
        generated fixture, illustration
      </p>

      <nav className="story__rail" data-id="rail" aria-label="Story scenes">
        {SCENES.map((s, i) => (
          <button key={s.name} className="story__tick" data-id={`tick-${i}`} aria-label={s.name} onClick={() => jump((s.start + (i ? 0.02 : 0)) * 6 * innerHeight)} style={{ top: `${(i / (SCENES.length - 1)) * 100}%` }} />
        ))}
        <span className="t-label story__rail-name on-map" data-id="rail-name" />
      </nav>
    </div>
  );
}
