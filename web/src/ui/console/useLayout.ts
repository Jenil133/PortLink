import { useEffect, useState } from "react";

export type Layout = "full" | "compact" | "phone";

const read = (): Layout => (innerWidth < 768 ? "phone" : innerWidth < 1280 ? "compact" : "full");

export function useLayout(): Layout {
  const [layout, setLayout] = useState<Layout>(read);
  useEffect(() => {
    const onResize = () => setLayout(read());
    addEventListener("resize", onResize);
    return () => removeEventListener("resize", onResize);
  }, []);
  return layout;
}
