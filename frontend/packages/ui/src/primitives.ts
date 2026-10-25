/**
 * Ant Design 原子组件白名单出口（§11.1）。
 * 业务代码只能从 @admin-frame/ui 导入，便于后续统一替换组件库。
 */
export {
  Alert,
  Badge,
  Button,
  Card,
  Descriptions,
  Divider,
  Form,
  Input,
  Select,
  Space,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';

/** 表格类型随组件一同从设计系统出口导出，避免业务代码直接依赖 antd（§11.1）。 */
export type { TableColumnsType, TableProps } from 'antd';
