import { describe, expect, it } from "vitest";
import { linkify } from "./linkify";

describe("linkify", () => {
  it("leaves text without URLs alone", () => {
    expect(linkify("no links here")).toEqual([{ text: "no links here" }]);
  });

  it("extracts URLs and strips trailing punctuation", () => {
    expect(linkify("listening on http://localhost:3000/, ready.")).toEqual([
      { text: "listening on " },
      { text: "http://localhost:3000/", href: "http://localhost:3000/" },
      { text: ", ready." },
    ]);
  });

  it("keeps balanced parens but drops an unbalanced closing one", () => {
    expect(linkify("(see https://en.wikipedia.org/wiki/Foo_(bar))")).toEqual([
      { text: "(see " },
      { text: "https://en.wikipedia.org/wiki/Foo_(bar)", href: "https://en.wikipedia.org/wiki/Foo_(bar)" },
      { text: ")" },
    ]);
  });
});
