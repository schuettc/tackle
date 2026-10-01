import { SEASONS, Cause, Color, helper } from "../src/consts";
import * as c from "../src/consts";

function localHelper() {
  return 1;
}

test("const", () => {
  expect(SEASONS).toEqual([2022, 2023, 2024]);
});

test("enum", () => {
  expect(Color.Red).toBe(0);
});

test("class reference", () => {
  expect(typeof Cause).toBe("function");
});

test("namespace constant", () => {
  expect(c.SEASONS.length).toBe(3);
});

test("project function", () => {
  expect(helper()).toBe(SEASONS[0]);
});

test("constructs class", () => {
  expect(new Cause()).toBeInstanceOf(Cause);
});

test("same-file helper", () => {
  expect(localHelper()).toBe(SEASONS[0]);
});

test("namespace call", () => {
  expect(c.helper()).toBe(SEASONS[0]);
});

test("fixture parameter", async ({ page }) => {
  expect(SEASONS).toBeDefined();
});

test("no code under test", () => {
  expect(1 + 1).toBe(2);
});
