'use client';

import { Megaphone } from 'lucide-react';
import { Card, CardContent } from '@/components/ui/card';

export default function AnnouncementsPage() {
  return (
    <div className="space-y-6">
      <h2 className="text-2xl font-bold">公告</h2>
      <Card>
        <CardContent className="flex flex-col items-center justify-center py-16 text-center">
          <Megaphone className="h-12 w-12 text-muted-foreground mb-4" />
          <p className="text-muted-foreground">暂无内容</p>
        </CardContent>
      </Card>
    </div>
  );
}
