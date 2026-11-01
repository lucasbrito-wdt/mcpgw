// Ported from design/v5/src/components/signature/nebula-shader.ts on 2026-10-31.
/* Shaders for NebulaField. One full-screen triangle; everything happens in the fragment shader. */

export const NEBULA_VERTEX = `
attribute vec2 a_position;
void main() { gl_Position = vec4(a_position, 0.0, 1.0); }
`;

// smoothstep edges must ascend (reversed edges are undefined in GLSL and render white on ANGLE).
// Colors are the @theme tokens in linear-ish 0..1: void #07061A, ink #0C0C0E, haze #3B2A7E, signal #5F5CFF.
export const NEBULA_FRAGMENT = `
#ifdef GL_FRAGMENT_PRECISION_HIGH
precision highp float;
#else
precision mediump float;
#endif

uniform vec2 u_resolution;
uniform float u_time;
uniform vec2 u_pointer;
uniform float u_intensity;

const vec3 VOID_COLOR = vec3(0.027, 0.024, 0.102);
const vec3 INK = vec3(0.047, 0.047, 0.055);
const vec3 HAZE = vec3(0.231, 0.165, 0.494);
const vec3 SIGNAL = vec3(0.373, 0.361, 1.0);

float hash(vec2 p) {
  p = fract(p * vec2(123.34, 456.21));
  p += dot(p, p + 45.32);
  return fract(p.x * p.y);
}

float noise(vec2 p) {
  vec2 i = floor(p);
  vec2 f = fract(p);
  vec2 u = f * f * (3.0 - 2.0 * f);
  return mix(mix(hash(i), hash(i + vec2(1.0, 0.0)), u.x), mix(hash(i + vec2(0.0, 1.0)), hash(i + vec2(1.0, 1.0)), u.x), u.y);
}

float fbm(vec2 p) {
  float value = 0.0;
  float amplitude = 0.5;
  mat2 rotate = mat2(0.8, 0.6, -0.6, 0.8);
  for (int i = 0; i < 5; i++) {
    value += amplitude * noise(p);
    p = rotate * p * 2.03;
    amplitude *= 0.5;
  }
  return value;
}

void main() {
  vec2 uv = gl_FragCoord.xy / u_resolution;
  float aspect = u_resolution.x / u_resolution.y;
  vec2 p = vec2(uv.x * aspect, uv.y);
  float t = u_time;

  // Space: deep indigo at the top settling into the app ink.
  vec3 color = mix(INK, VOID_COLOR, smoothstep(0.05, 0.9, uv.y));
  color += HAZE * 0.34 * u_intensity * (1.0 - smoothstep(0.0, 1.5, distance(p, vec2(0.12 * aspect, 0.8))));

  // Clouds: domain-warped fbm drifting slowly, pooled in the lower half like the art.
  vec2 warp = vec2(fbm(p * 1.25 + vec2(0.0, t * 0.016)), fbm(p * 1.25 + vec2(5.2, 1.3) - t * 0.011));
  float n = fbm(p * 1.55 + 1.8 * warp + vec2(t * 0.019, 0.0));
  float pool = 1.0 - smoothstep(0.0, 0.78, uv.y);
  float cloud = smoothstep(0.32, 0.86, n) * pool;

  // Pointer light, eased on the CPU side, brightening the clouds it passes.
  vec2 pointer = vec2(u_pointer.x * aspect, u_pointer.y);
  float d = distance(p, pointer);
  float light = exp(-d * d * 4.5);

  color += HAZE * cloud * (1.0 + 0.7 * light) * u_intensity;
  color += SIGNAL * pow(cloud, 2.4) * 0.26 * u_intensity;
  color += SIGNAL * light * 0.045 * u_intensity;
  color += HAZE * (1.0 - smoothstep(0.0, 0.42, uv.y)) * 0.32 * u_intensity;

  // Fine grain so the gradient never bands.
  float grain = hash(gl_FragCoord.xy + fract(t * 7.13) * 91.7) - 0.5;
  color += grain * 0.022;

  gl_FragColor = vec4(color, 1.0);
}
`;
