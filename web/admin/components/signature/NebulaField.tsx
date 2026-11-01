// Ported from design/v5/src/components/signature/NebulaField.tsx on 2026-10-31.
"use client";

import { useEffect, useRef } from "react";

import { cn } from "../../lib/utils";

import { NEBULA_FRAGMENT, NEBULA_VERTEX } from "./nebula-shader";

export interface NebulaFieldProps {
  /** Cloud and light strength: 1 on the portal, about 0.35 on the admin. Changes ease in, not jump. */
  intensity?: number;
  className?: string;
}

const MAX_DPR = 1.5;
const POINTER_EASE = 0.045;
const INTENSITY_EASE = 0.04;
const STATIC_TIME = 14;

interface GlProgram {
  gl: WebGLRenderingContext;
  uniforms: { resolution: WebGLUniformLocation | null; time: WebGLUniformLocation | null; pointer: WebGLUniformLocation | null; intensity: WebGLUniformLocation | null };
}

function compile(gl: WebGLRenderingContext, type: number, source: string): WebGLShader {
  const shader = gl.createShader(type);
  if (!shader) throw new Error("createShader failed");
  gl.shaderSource(shader, source);
  gl.compileShader(shader);
  if (!gl.getShaderParameter(shader, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(shader) ?? "shader compile failed");
  return shader;
}

function createProgram(canvas: HTMLCanvasElement): GlProgram | null {
  const gl = canvas.getContext("webgl", { alpha: false, antialias: false, depth: false, stencil: false, powerPreference: "low-power" });
  if (!gl) return null;
  const program = gl.createProgram();
  if (!program) return null;
  gl.attachShader(program, compile(gl, gl.VERTEX_SHADER, NEBULA_VERTEX));
  gl.attachShader(program, compile(gl, gl.FRAGMENT_SHADER, NEBULA_FRAGMENT));
  gl.linkProgram(program);
  if (!gl.getProgramParameter(program, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(program) ?? "link failed");
  gl.useProgram(program);

  // One triangle that covers the whole viewport.
  gl.bindBuffer(gl.ARRAY_BUFFER, gl.createBuffer());
  gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
  const position = gl.getAttribLocation(program, "a_position");
  gl.enableVertexAttribArray(position);
  gl.vertexAttribPointer(position, 2, gl.FLOAT, false, 0, 0);

  return {
    gl,
    uniforms: {
      resolution: gl.getUniformLocation(program, "u_resolution"),
      time: gl.getUniformLocation(program, "u_time"),
      pointer: gl.getUniformLocation(program, "u_pointer"),
      intensity: gl.getUniformLocation(program, "u_intensity"),
    },
  };
}

/**
 * Fixed WebGL nebula behind the whole app: fbm violet clouds pooled at the bottom, slow drift,
 * a pointer light with inertia and fine grain. Pauses when the tab is hidden, renders one still
 * frame under prefers-reduced-motion and falls back to CSS gradients without WebGL.
 */
export function NebulaField({ intensity = 1, className }: NebulaFieldProps) {
  const hostRef = useRef<HTMLDivElement>(null);
  const fallbackRef = useRef<HTMLDivElement>(null);
  const intensityRef = useRef(intensity);
  const redrawRef = useRef<() => void>(() => {});

  useEffect(() => {
    intensityRef.current = intensity;
    // CSSOM write (not a style attribute) so the CSP style-src 'self' holds; the fallback fades in from 0.
    fallbackRef.current?.style.setProperty("opacity", String(intensity));
    redrawRef.current();
  }, [intensity]);

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    // A fresh canvas per effect run: a context lost in cleanup can never be revived on the same element
    // (Strict Mode runs effects twice in dev), so the canvas lives and dies with the effect.
    const canvas = document.createElement("canvas");
    canvas.className = "absolute inset-0 size-full opacity-0 transition-opacity duration-700 ease-switch";
    host.appendChild(canvas);
    let glProgram: GlProgram | null = null;
    try {
      glProgram = createProgram(canvas);
    } catch {
      glProgram = null;
    }
    if (!glProgram) {
      canvas.remove();
      return;
    }
    const { gl, uniforms } = glProgram;
    const motionQuery = window.matchMedia("(prefers-reduced-motion: reduce)");

    let frame = 0;
    let elapsed = 0;
    let lastTick = performance.now();
    let currentIntensity = intensityRef.current;
    const pointer = { x: 0.72, y: 0.22 };
    const target = { x: 0.72, y: 0.22 };

    function resize() {
      const dpr = Math.min(window.devicePixelRatio || 1, MAX_DPR);
      canvas.width = Math.round(canvas.clientWidth * dpr);
      canvas.height = Math.round(canvas.clientHeight * dpr);
      gl.viewport(0, 0, canvas.width, canvas.height);
    }

    function draw(time: number) {
      gl.uniform2f(uniforms.resolution, canvas.width, canvas.height);
      gl.uniform1f(uniforms.time, time);
      gl.uniform2f(uniforms.pointer, pointer.x, pointer.y);
      gl.uniform1f(uniforms.intensity, currentIntensity);
      gl.drawArrays(gl.TRIANGLES, 0, 3);
    }

    function drawStill() {
      currentIntensity = intensityRef.current;
      draw(STATIC_TIME);
    }

    function tick(now: number) {
      elapsed += Math.min(now - lastTick, 100) / 1000;
      lastTick = now;
      pointer.x += (target.x - pointer.x) * POINTER_EASE;
      pointer.y += (target.y - pointer.y) * POINTER_EASE;
      currentIntensity += (intensityRef.current - currentIntensity) * INTENSITY_EASE;
      draw(STATIC_TIME + elapsed);
      frame = requestAnimationFrame(tick);
    }

    function start() {
      cancelAnimationFrame(frame);
      if (motionQuery.matches) {
        drawStill();
        return;
      }
      lastTick = performance.now();
      frame = requestAnimationFrame(tick);
    }

    function onPointerMove(event: PointerEvent) {
      target.x = event.clientX / window.innerWidth;
      target.y = 1 - event.clientY / window.innerHeight;
    }

    function onVisibilityChange() {
      if (document.hidden) cancelAnimationFrame(frame);
      else start();
    }

    function onContextLost(event: Event) {
      event.preventDefault();
      cancelAnimationFrame(frame);
      canvas.style.opacity = "0";
    }

    const resizeObserver = new ResizeObserver(() => {
      resize();
      if (motionQuery.matches) drawStill();
    });
    resizeObserver.observe(canvas);
    resize();
    redrawRef.current = () => {
      if (motionQuery.matches) drawStill();
    };
    window.addEventListener("pointermove", onPointerMove, { passive: true });
    document.addEventListener("visibilitychange", onVisibilityChange);
    motionQuery.addEventListener("change", start);
    canvas.addEventListener("webglcontextlost", onContextLost);
    start();
    // Fade the canvas in over the CSS fallback once the first frame is drawn.
    requestAnimationFrame(() => canvas.style.setProperty("opacity", "1"));

    return () => {
      cancelAnimationFrame(frame);
      resizeObserver.disconnect();
      redrawRef.current = () => {};
      window.removeEventListener("pointermove", onPointerMove);
      document.removeEventListener("visibilitychange", onVisibilityChange);
      motionQuery.removeEventListener("change", start);
      canvas.removeEventListener("webglcontextlost", onContextLost);
      gl.getExtension("WEBGL_lose_context")?.loseContext();
      canvas.remove();
    };
  }, []);

  return (
    <div ref={hostRef} aria-hidden className={cn("pointer-events-none fixed inset-0 -z-10 overflow-hidden bg-linear-to-b from-void to-ink", className)}>
      {/* CSS fallback when WebGL is missing; the effect appends the canvas above it. */}
      <div
        ref={fallbackRef}
        className="absolute inset-0 opacity-0 bg-[radial-gradient(120%_60%_at_60%_108%,var(--color-haze),transparent_70%),radial-gradient(60%_40%_at_75%_100%,color-mix(in_oklab,var(--color-signal)_35%,transparent),transparent_70%)] transition-opacity duration-700 ease-switch"
      />
    </div>
  );
}
