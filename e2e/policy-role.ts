export function migratePolicyRoles(value: string): { value: string; replacements: number } {
  let replacements = 0;
  const lines = value.split("\n").map((line) => {
    const fields = line.split(",");
    const kind = fields[0]?.trim();
    if (kind !== "p" && kind !== "g" && kind !== "g2") return line;

    for (let i = 1; i < fields.length; i += 1) {
      if (fields[i]?.trim() !== "consumer") continue;
      fields[i] = fields[i].replace("consumer", "customer");
      replacements += 1;
    }
    return fields.join(",");
  });
  return { value: lines.join("\n"), replacements };
}
