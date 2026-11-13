import { BlockPage } from "@/components/couple/block-page";

export default async function Page({ params }: { params: Promise<{ block: string }> }) {
  const { block } = await params;
  return <BlockPage id={block} />;
}
