// Level definitions and a procedural generator for endless play.

export const COLS = 8;

// Brick opcodes used in level strings:
//   .  empty          #  standard (blue)     r  standard (red)
//   g  standard (green)
//   y  gold (falls as a power-up)
//   o  hole (smash through to the roof)
//   a  angle (only breaks on a glancing hit)
//   s  speed (needs a fast ball)
//   G  glass (reveals the map when hit)
//   ^  gem (freezes a damaged brick)
export const LEVELS = [
  {
    name: "First Ascent",
    rows: [
      ".o####o.",
      ".#.#.#.#",
      "..####..",
      "........",
    ],
  },
  {
    name: "The Gauntlet",
    rows: [
      "##.##.##",
      "..####..",
      "go....og",
      "##.##.##",
      "........",
    ],
  },
  {
    name: "Angle Master",
    rows: [
      "aaa...aa",
      "aa...aa.",
      "...##...",
      ".######.",
      "........",
    ],
  },
  {
    name: "Need for Speed",
    rows: [
      "ssssssss",
      "...##...",
      "..####..",
      ".######.",
      "........",
    ],
  },
  {
    name: "Gem & Glass",
    rows: [
      "^#G#.#G#",
      "G.#^#.#G",
      ".####G..",
      "..####..",
      "........",
    ],
  },
  {
    name: "The Wall",
    rows: [
      "########",
      ".######.",
      "..####..",
      "o..##..o",
      ".o....o.",
      "........",
    ],
  },
];

// Procedural levels for level index >= LEVELS.length (1-based level number n)
export function generateLevel(n) {
  const t = n - LEVELS.length; // how many levels past the hand-made ones
  const rows = [];
  const nRows = Math.min(4 + Math.floor(t / 2), 7);
  const density = Math.min(0.42 + t * 0.05, 0.72);
  const chaos = Math.min(0.04 + t * 0.03, 0.5);

  const pool = ["#", "#", "#", "r", "g"];
  const specials = [];
  if (t >= 1) specials.push("o");
  if (t >= 2) specials.push("a");
  if (t >= 3) specials.push("s");
  if (t >= 2) specials.push("y");
  if (t >= 4) specials.push("G");
  if (t >= 5) specials.push("^");

  for (let i = 0; i < nRows; i++) {
    let row = "";
    for (let c = 0; c < COLS; c++) {
      const r = Math.random();
      let ch;
      if (r < density * 0.78) {
        ch = pool[Math.floor(Math.random() * pool.length)];
      } else if (r < density) {
        ch = specials[Math.floor(Math.random() * specials.length)] || "#";
      } else {
        ch = ".";
      }
      // sprinkle a little chaos so bricks don't always line up perfectly
      if (Math.random() < chaos && ch !== ".") {
        ch = pool[Math.floor(Math.random() * pool.length)];
      }
      row += ch;
    }
    rows.push(row);
  }
  rows.push("........".slice(0, COLS));
  rows.push("........".slice(0, COLS));
  return { name: `Deep Field ${n}`, rows };
}
