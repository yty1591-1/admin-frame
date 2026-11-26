export interface PageProps {
  /**
   * @deprecated 菜单页不再渲染顶栏标题；保留字段以免破坏既有调用方类型。
   */
  title?: string;
  /**
   * @deprecated 菜单页不再渲染顶栏说明；保留字段以免破坏既有调用方类型。
   */
  description?: string;
  contentClass?: string;
  /**
   * 根据content可见高度自适应
   */
  autoContentHeight?: boolean;
  /**
   * @deprecated 顶栏已移除，headerClass 不再生效。
   */
  headerClass?: string;
  footerClass?: string;
  /**
   * Custom height offset value (in pixels) to adjust content area sizing
   * when used with autoContentHeight
   * @default 0
   */
  heightOffset?: number;
  /**
   * Whether the footer is anchored to the bottom of the page layout.
   * The footer remains in flow so it cannot obscure page content.
   * @default false
   */
  footerFixed?: boolean;
}
