import { BIG, SMALL, bigCall } from "../src/big";

test("skips big reference", () => {
  expect(BIG).not.toBe(SMALL);
});

test("big call target truncates", () => {
  expect(bigCall()).toBeTruthy();
});
