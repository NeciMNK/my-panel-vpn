import { describe, expect, it } from "vitest";

import { aiOnlyAllowed, fixedExitDown } from "@/pages/nodes/FixedExitsCard";

describe("fixedExitDown", () => {
  const nodes = [
    { id: 2, enable: true, status: "online" },
    { id: 3, enable: true, status: "offline" },
    { id: 4, enable: false, status: "online" },
  ];

  it("flags a pinned exit the sticky pass skips", () => {
    expect([2, 3, 4, 9].map((id) => fixedExitDown(id, nodes))).toEqual([
      false,
      true,
      true,
      true,
    ]);
  });

  it("never flags this panel, which has no node row", () => {
    expect(fixedExitDown(0, [])).toBe(false);
  });
});

describe("aiOnlyAllowed", () => {
  it("offers AI sites only just where the backend accepts it: a server with a country", () => {
    const nodes = [{ id: 2, country: "DE" }, { id: 3, country: "" }, { id: 4 }];
    expect(
      [undefined, 0, 2, 3, 4, 9].map((id) => aiOnlyAllowed(id, nodes)),
    ).toEqual([false, false, true, false, false, false]);
  });
});
