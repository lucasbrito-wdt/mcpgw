import { Geist_Mono, Instrument_Sans, Martian_Mono } from "next/font/google";

// next/font downloads the files at build time and self-hosts them, so the CSP keeps font-src 'self'.
export const instrumentSans = Instrument_Sans({ variable: "--font-instrument-sans", subsets: ["latin"], weight: ["400", "500", "600"], display: "swap" });
// Brand font: wordmark and splash title only. Too wide for tables, so data stays on Geist Mono.
export const martianMono = Martian_Mono({ variable: "--font-martian-mono", subsets: ["latin"], weight: ["400", "600"], display: "swap" });
export const geistMono = Geist_Mono({ variable: "--font-geist-mono", subsets: ["latin"], display: "swap" });
