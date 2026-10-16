// Small pieces the live session and its dock both use.

export type Rest = { sessionId: string; startedAt: number; target: number };

export const round2 = (n: number) => Math.round(n * 100) / 100;
