import { mount } from '@vue/test-utils';

import { describe, expect, it } from 'vitest';

import { Page } from '..';

describe('page.vue', () => {
  it('does not render page header for title or description', () => {
    const wrapper = mount(Page, {
      props: {
        title: 'Test Title',
        description: 'Test Description',
      },
    });

    expect(wrapper.find('.border-b').exists()).toBe(false);
    expect(wrapper.text()).not.toContain('Test Title');
    expect(wrapper.text()).not.toContain('Test Description');
  });

  it('renders default slot content', () => {
    const wrapper = mount(Page, {
      slots: {
        default: '<p>Default Slot Content</p>',
      },
    });

    expect(wrapper.html()).toContain('<p>Default Slot Content</p>');
  });

  it('renders footer slot when provided', () => {
    const wrapper = mount(Page, {
      slots: {
        footer: '<p>Footer Slot Content</p>',
      },
    });

    expect(wrapper.html()).toContain('<p>Footer Slot Content</p>');
  });

  it('applies the custom contentClass', () => {
    const wrapper = mount(Page, {
      props: {
        contentClass: 'custom-class',
      },
    });

    const contentDiv = wrapper.find('.p-4');
    expect(contentDiv.classes()).toContain('custom-class');
  });

  it('uses flex layout for automatic content height', () => {
    const wrapper = mount(Page, {
      props: {
        autoContentHeight: true,
        heightOffset: 12,
      },
    });

    const content = wrapper.find('[data-layout-region="page-content"]');

    expect(wrapper.classes()).toContain('min-h-0');
    expect(wrapper.classes()).toContain('overflow-hidden');
    expect(content.exists()).toBe(true);
    expect(content.classes()).toContain('min-h-0');
    expect(content.classes()).toContain('flex-1');
    expect(content.classes()).toContain('overflow-y-auto');
    expect(content.attributes('style')).toContain(
      '--page-content-height-offset: 12px',
    );
    expect(content.attributes('style')).not.toContain('--vben-content-height');
  });

  it('positions the footer according to footerFixed', async () => {
    const wrapper = mount(Page, {
      slots: {
        footer: '<p>Footer</p>',
      },
    });

    const footer = wrapper.find('.bg-card');
    expect(footer.classes()).toContain('shrink-0');
    expect(footer.classes()).not.toContain('absolute');

    await wrapper.setProps({ footerFixed: true });

    expect(footer.classes()).toContain('mt-auto');
    expect(footer.classes()).toContain('shrink-0');
    expect(footer.classes()).not.toContain('absolute');
  });
});
