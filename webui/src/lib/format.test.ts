import { describe, expect, it } from "vitest"

import {
  describeEffectiveQuota,
  describeEffectiveRate,
  describeFiles,
  describeQuota,
  describeRate,
  formatBytes,
} from "@/lib/format"

describe("formatBytes", () => {
  it.each([
    [0, "0 B"],
    [1, "1 B"],
    [512, "512 B"],
    [1023, "1023 B"],
    [1024, "1.0 KB"],
    [1536, "1.5 KB"],
    [10240, "10 KB"],
    [1048576, "1.0 MB"],
    [1073741824, "1.0 GB"],
    [1099511627776, "1.0 TB"],
  ])("formats %d as %s", (value, expected) => {
    expect(formatBytes(value)).toBe(expected)
  })

  it("never grows past the last unit", () => {
    expect(formatBytes(1099511627776 * 1024)).toBe("1024 TB")
  })

  it("renders non-positive and invalid values as 0 B", () => {
    expect(formatBytes(-1)).toBe("0 B")
    expect(formatBytes(Number.NaN)).toBe("0 B")
  })
})

describe("describeQuota (stored: 0 inherits, -1 unlimited)", () => {
  it("inherits for 0 and undefined", () => {
    expect(describeQuota(0)).toBe("Default")
    expect(describeQuota(undefined)).toBe("Default")
  })

  it("supports a custom inherit label", () => {
    expect(describeQuota(0, "Group default")).toBe("Group default")
  })

  it("marks -1 unlimited and formats positives", () => {
    expect(describeQuota(-1)).toBe("Unlimited")
    expect(describeQuota(2048)).toBe("2.0 KB")
  })
})

describe("describeEffectiveQuota (enforced: 0 unlimited)", () => {
  it("treats 0 and undefined as unlimited", () => {
    expect(describeEffectiveQuota(0)).toBe("Unlimited")
    expect(describeEffectiveQuota(undefined)).toBe("Unlimited")
  })

  it("formats enforced values", () => {
    expect(describeEffectiveQuota(10 * 1024 * 1024)).toBe("10 MB")
  })
})

describe("describeRate (stored: 0 inherits, -1 unlimited)", () => {
  it("inherits for 0 and undefined", () => {
    expect(describeRate(0)).toBe("Default")
    expect(describeRate(undefined)).toBe("Default")
  })

  it("marks -1 unlimited and formats positives per second", () => {
    expect(describeRate(-1)).toBe("Unlimited")
    expect(describeRate(2048)).toBe("2.0 KB/s")
  })
})

describe("describeEffectiveRate (enforced: 0 unlimited)", () => {
  it("treats 0 and undefined as unlimited", () => {
    expect(describeEffectiveRate(0)).toBe("Unlimited")
    expect(describeEffectiveRate(undefined)).toBe("Unlimited")
  })

  it("formats enforced values per second", () => {
    expect(describeEffectiveRate(512 * 1024)).toBe("512 KB/s")
  })
})

describe("describeFiles (stored: 0 inherits, -1 unlimited)", () => {
  it("inherits for 0 and undefined", () => {
    expect(describeFiles(0)).toBe("Default")
    expect(describeFiles(undefined)).toBe("Default")
  })

  it("marks -1 unlimited and formats positive counts", () => {
    expect(describeFiles(-1)).toBe("Unlimited")
    expect(describeFiles(1500)).toBe(`${(1500).toLocaleString()} files`)
  })
})
