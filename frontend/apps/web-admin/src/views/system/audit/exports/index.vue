<script lang="ts" setup>
import type { AuditExportJob } from '#/api';

import { onMounted, reactive, ref } from 'vue';
import { useRoute } from 'vue-router';

import { Page } from '@vben/common-ui';

import { Button, Input, message, Tag } from 'ant-design-vue';

import { getAuditExportJobApi } from '#/api';

defineOptions({ name: 'AuditExports' });

const route = useRoute();
const querying = ref(false);
const filters = reactive({
  export_id: '',
});
const job = ref<AuditExportJob>();

const statusMap: Record<string, { color: string; label: string }> = {
  expired: { color: 'default', label: '已过期' },
  failed: { color: 'error', label: '失败' },
  pending: { color: 'warning', label: '排队中' },
  running: { color: 'processing', label: '处理中' },
  succeeded: { color: 'success', label: '已完成' },
};

async function queryJob() {
  const id = filters.export_id.trim();
  if (!id) {
    message.warning('请输入导出任务 ID');
    return;
  }
  querying.value = true;
  try {
    job.value = await getAuditExportJobApi(id);
  } catch {
    // 错误已由全局请求拦截器提示
  } finally {
    querying.value = false;
  }
}

onMounted(() => {
  const fromQuery = String(route.query.export_id ?? '').trim();
  if (!fromQuery) {
    return;
  }
  filters.export_id = fromQuery;
  queryJob();
});
</script>

<template>
  <Page>
    <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_428px]">
      <div class="bg-card rounded-lg p-5">
        <h3 class="mb-4 text-base font-semibold">填写信息</h3>
        <div class="mb-2 text-sm font-medium">导出任务 ID</div>
        <Input
          v-model:value="filters.export_id"
          allow-clear
          class="mb-6"
          placeholder="输入导出任务 ID"
        />
        <p class="text-muted-foreground mb-4 text-sm">
          任务由审计事件页面按筛选条件创建，记录当前会话提交结果。
        </p>
        <Button :loading="querying" type="primary" @click="queryJob">
          查询任务
        </Button>
      </div>

      <div class="bg-card rounded-lg p-5">
        <h3 class="mb-4 text-base font-semibold">任务结果</h3>
        <template v-if="job">
          <div class="mb-4">
            <Tag :color="statusMap[job.status]?.color ?? 'default'">
              {{ statusMap[job.status]?.label ?? job.status }}
            </Tag>
          </div>
          <div class="space-y-3 text-sm">
            <div class="flex justify-between gap-4">
              <span class="text-muted-foreground">任务 ID</span>
              <span class="break-all font-medium">{{ job.export_id }}</span>
            </div>
            <div class="flex justify-between gap-4">
              <span class="text-muted-foreground">行数</span>
              <span class="font-medium">{{ job.rows }}</span>
            </div>
            <div class="flex justify-between gap-4">
              <span class="text-muted-foreground">创建于</span>
              <span class="font-medium">{{ job.created_at }}</span>
            </div>
            <div v-if="job.expires_at" class="flex justify-between gap-4">
              <span class="text-muted-foreground">有效期</span>
              <span class="font-medium">{{ job.expires_at }}</span>
            </div>
            <div v-if="job.error" class="text-destructive">
              {{ job.error }}
            </div>
          </div>
          <div class="mt-6 border-t pt-4">
            <a
              v-if="job.download_path && job.status === 'succeeded'"
              :href="job.download_path"
              class="ant-btn ant-btn-primary"
              rel="noopener noreferrer"
              target="_blank"
            >
              下载 CSV
            </a>
            <p v-else class="text-muted-foreground text-sm">
              {{
                job.status === 'succeeded'
                  ? '任务已完成，暂无下载地址'
                  : '处理完成后可在此下载'
              }}
            </p>
          </div>
        </template>
        <p v-else class="text-muted-foreground text-sm">
          提交有效任务 ID 后，在此查看处理状态、行数与下载入口。
        </p>
      </div>
    </div>
  </Page>
</template>
