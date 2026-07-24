import { TokenMPLogoMark } from '@/components/tokenmp-logo';

export default function Home() {
  return (
    <main className="flex min-h-dvh flex-col items-center justify-center gap-6 p-10">
      <TokenMPLogoMark className="h-16 w-16" />
      <h1 className="text-2xl font-bold">TokenMP</h1>
      <p className="text-muted-foreground">AI 模型 API 网关</p>
      <div className="flex gap-3">
        <a href="/login" className="rounded-md border px-4 py-2 text-sm hover:bg-muted">登录</a>
        <a href="/register" className="rounded-md border px-4 py-2 text-sm hover:bg-muted">注册</a>
      </div>
    </main>
  );
}
