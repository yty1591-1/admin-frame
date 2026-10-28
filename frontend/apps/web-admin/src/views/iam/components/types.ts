export type FormFieldComponent =
  | 'datetime'
  | 'input'
  | 'json'
  | 'number'
  | 'select'
  | 'switch'
  | 'tags'
  | 'textarea';

export interface FormFieldOption {
  label: string;
  value: number | string;
}

export interface FormField {
  component?: FormFieldComponent;
  disabled?: boolean;
  label: string;
  max?: number;
  min?: number;
  name: string;
  options?: FormFieldOption[];
  placeholder?: string;
  required?: boolean;
  rows?: number;
  span?: 1 | 2;
}
