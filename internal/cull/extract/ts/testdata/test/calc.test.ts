import { add, multiply } from "../src/calc";

function helperValue(): number {
  return 41;
}

test("adds two numbers", () => {
  expect(add(2, 3)).toBe(5);
});

describe("multiply", () => {
  it("multiplies positive numbers", () => {
    expect(multiply(2, 3)).toBe(6);
  });

  describe("nested edge cases", () => {
    it("multiplies by zero", () => {
      expect(multiply(2, 0)).toBe(0);
    });
  });
});

test("uses a helper", () => {
  expect(helperValue()).toBe(41);
});
