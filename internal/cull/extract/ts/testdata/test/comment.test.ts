import { add } from "../src/calc";

// a comment right before the test
test("uses add with a leading comment", () => {
  expect(add(1, 1)).toBe(2);
});
