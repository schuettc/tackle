import { SEASONS, Cause, Color } from "../src/consts";
import * as c from "../src/consts";

test("const", () => {
  expect(SEASONS).toEqual([2022, 2023, 2024]);
});

test("class", () => {
  expect(new Cause()).toBeInstanceOf(Cause);
});

test("enum", () => {
  expect(Color.Red).toBe(0);
});

test("namespace", () => {
  expect(c.helper()).toBe(1);
});
