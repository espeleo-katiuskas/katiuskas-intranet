// What the API's error codes say, in Spanish. The API's own messages are in English (they are for
// whoever reads the logs or calls the API), so `apiFetch` replaces them with these when it knows the
// code; for one it does not know, the API's message is shown as it is. A page that wants to say
// something more specific for a code (distributionCommon.js's apiErrorText, accountTree.js's
// accountingErrorText) still can: it reads `err.code`.
//
// A text is a string, or a function of the API's message for the codes whose message carries
// something worth showing (the names of the accounts missing, the totals that do not match).

// What follows the first ": " of the API's message, if anything (names, ids...).
const detailOf = (message) => {
  const i = (message ?? '').indexOf(': ');
  return i === -1 ? '' : message.slice(i + 2);
};
const withDetail = (text) => (message) => {
  const detail = detailOf(message);
  return detail ? `${text} (${detail})` : text;
};

const texts = {
  // General
  internal_error: 'Ha ocurrido un error inesperado en el servidor. Inténtalo de nuevo más tarde.',
  rate_limited: 'Demasiadas peticiones desde esta dirección. Espera un poco e inténtalo de nuevo.',
  unauthorized: 'Tu sesión no es válida o ha caducado. Vuelve a entrar.',
  forbidden: 'No tienes permiso para hacer esto.',
  not_found: 'No se ha encontrado lo que buscabas.',
  invalid_body: 'Los datos enviados no son válidos.',
  invalid_id: 'El identificador no es válido.',
  invalid_date: 'La fecha no es válida.',
  invalid_date_range: 'La fecha de fin no puede ser anterior a la de inicio.',
  invalid_item_code:
    'El código no puede ser solo números: añádele alguna letra o un guion (por ejemplo, C-12).',
  duplicate_activity: 'Ya hay una actividad con ese título que empieza ese día.',
  invalid_type: 'El tipo no es válido.',
  invalid_name: 'Hace falta un nombre válido.',
  invalid_status: 'El estado no es válido.',
  invalid_year: 'El año tiene que estar entre 1900 y 2200.',
  invalid_limit: 'El número de resultados por página no es válido.',
  invalid_offset: 'La posición de la página no es válida.',
  invalid_order: 'El orden no es válido.',
  invalid_sort: 'La columna de ordenación no es válida.',
  invalid_direction: 'La dirección del movimiento no es válida.',
  missing_fields: 'Faltan datos obligatorios.',
  duplicate: 'Ya existe otro registro con esos datos: no se pueden repetir.',
  reorder_mismatch:
    'La lista ha cambiado mientras la ordenabas. Recarga la página e inténtalo de nuevo.',
  file_required: 'Elige un fichero que no esté vacío.',
  file_too_large: 'El fichero es demasiado grande.',
  invalid_path: 'Esa ruta no es válida.',
  not_a_directory: 'Eso es un fichero, no una carpeta.',
  not_a_file: 'Eso es una carpeta, no un fichero.',

  // Sign-in and sessions
  invalid_token: 'El enlace de acceso no es válido o ha caducado. Pide uno nuevo.',
  invalid_code: 'El código no es válido o ha caducado.',
  unknown_details: 'No hay ninguna persona con ese correo y ese teléfono que tenga acceso.',
  not_impersonating: 'Esta sesión no es una impersonación.',
  cannot_impersonate_self: 'No puedes impersonarte a ti mismo.',
  not_eligible: 'Esa persona no puede usar la intranet, así que no se puede entrar como ella.',
  impersonating: 'Una sesión impersonada no puede cambiar de roles.',
  invalid_roles: 'Elige al menos un rol.',
  unknown_role: 'Alguno de los roles no existe.',
  no_baseline_role:
    'Hace falta al menos Administrador, Junta o Socio: sin ninguno no se puede usar nada.',

  // People, roles, membership
  invalid_login: 'El login solo puede llevar letras minúsculas, números, puntos y guiones.',
  has_references:
    'No se puede borrar: la persona todavía tiene datos asociados (roles, periodos de socio, licencias, actividades, cuotas…). Quítaselos antes.',
  cannot_delete_self: 'No puedes borrar tu propia cuenta.',
  restricted_fields:
    'Solo puedes cambiar tus datos de contacto; el resto lo cambia un administrador.',
  automatic_role: 'Ese rol es automático: no se concede ni se quita a mano.',
  system_role: 'Ese rol es del sistema y no se puede borrar.',
  role_in_use: 'Ese rol lo tiene alguien todavía: quítaselo antes de borrarlo.',
  last_admin: 'No se puede quitar el rol de administrador al único administrador que queda.',
  self_admin_removal: 'Un administrador no puede quitarse a sí mismo el rol de administrador.',
  overlapping_period: 'Ese periodo se solapa con otro que ya existe para esta persona.',
  outside_membership_period:
    'La baja temporal tiene que caer dentro de un periodo en el que la persona fue socia.',
  start_date_required:
    'Hace falta la fecha de inicio, porque la persona aún no tiene ningún periodo de socio.',
  due_already_recorded: 'Esa cuota ya está registrada para esa persona y ese año.',
  unknown_transaction: 'La transacción indicada no existe.',
  transaction_not_for_dues:
    'La transacción no tiene ningún apunte en una cuenta que admita cuotas anuales.',
  transaction_not_for_licenses:
    'La transacción no tiene ningún apunte en una cuenta que admita licencias.',
  invalid_position: 'El cargo no es válido.',
  invalid_license_type: 'El tipo de licencia no es válido.',
  invalid_other_detail: 'Una licencia «Otra» necesita que digas cuál es, y las demás no.',
  not_active_member:
    'La persona tiene que haber sido socia en algún momento del año de la licencia.',

  // Activities
  invalid_visibility: 'La visibilidad no es válida.',
  duplicate_holiday: 'Ya hay un festivo en esa fecha.',
  not_a_member:
    'Solo un socio puede apuntarse a una actividad (y quien la apunta tiene que serlo).',
  unknown_participant: 'Alguno de los participantes no existe.',
  title_required: 'Hace falta un título.',
  description_required: 'Un recurso de texto necesita una descripción.',
  description_not_allowed: 'Solo un recurso de texto puede tener descripción.',
  invalid_fields: 'Los datos no corresponden al tipo de recurso.',
  invalid_activity_id: 'La actividad indicada no es válida.',

  // Material
  duplicate_item_code: 'Ya hay otro material con ese código.',
  invalid_item_fields: 'Los datos no corresponden al tipo de material elegido.',
  model_without_brand: 'Un modelo solo se puede indicar si hay marca.',
  single_unit_item: 'Las cuerdas y las sacas son siempre una sola unidad.',
  invalid_days: 'El número de días tiene que estar entre 1 y 730.',
  duplicate_material:
    'Ya hay otro material exactamente igual (mismo tipo, marca, modelo, código, descripción y atributos). Si tienes más unidades, súmalas a ese material.',
  item_description_required: 'La descripción es obligatoria para este tipo de material.',
  missing_attribute: 'Falta un atributo obligatorio del tipo de material.',
  invalid_attribute: 'Alguno de los atributos no corresponde al tipo de material elegido.',
  duplicate_attribute: 'Ese tipo ya tiene un atributo con ese nombre.',
  duplicate_option: 'Ese atributo ya tiene una opción con ese nombre.',
  option_in_use: 'Hay material con esa opción: no se puede quitar.',
  would_duplicate_items:
    'Con este cambio habría dos materiales exactamente iguales del mismo tipo: distínguelos antes.',
  duplicate_type: 'Ya hay otro tipo de material con ese nombre.',
  type_in_use: 'El tipo tiene material y no se puede borrar.',
  fixed_type: 'Los tipos «Cuerda» y «Saca» son fijos: no se pueden renombrar ni borrar.',
  item_has_movements:
    'Este material tiene movimientos (préstamos, ventas, pérdidas…) y no se puede borrar.',
  movement_needed:
    'No se puede borrar este movimiento: otros movimientos dependen de él (se quedaría el stock en negativo o una devolución sin préstamo).',
  activity_in_use:
    'La actividad todavía tiene participantes, recursos o movimientos de material: quítalos antes de borrarla.',
  has_guests: 'Esa persona tiene invitados en la actividad: quítalos antes.',
  duplicate_guest: 'Esa persona ya ha apuntado a un invitado con ese nombre.',
  inviter_not_signed_up: 'Quien invita tiene que estar apuntado a la actividad.',
  invited_by_required: 'Hay que indicar cuál de los apuntados invita.',
  activity_full: 'La actividad está completa: no se puede apuntar a nadie más.',
  max_below_attendance:
    'El máximo no puede ser menor que las personas que ya van (socios e invitados).',
  invalid_max_participants: 'El máximo de apuntados tiene que ser un número de 1 o más.',
  kind_change:
    'Una cuerda o una saca no puede cambiar de tipo, ni el material general pasar a ser cuerda o saca.',
  unknown_type: 'El tipo de material no existe.',
  insufficient_stock: 'No queda cantidad suficiente disponible.',
  activity_required: 'Los préstamos y las devoluciones tienen que ir asociados a una actividad.',
  login_required: 'Los alquileres necesitan indicar a qué persona se hacen.',
  not_loanable: 'Este material no se puede prestar.',
  not_rentable: 'Este material no se puede alquilar (no tiene precio de alquiler).',
  not_sellable: 'Este material no se puede vender (no tiene precio de venta).',
  return_exceeds_loaned: 'No se puede devolver más de lo que hay prestado para esta actividad.',
  recipient_not_member: 'Un préstamo o un alquiler solo puede ser para un socio.',
  recipient_not_member_or_former: 'Una venta solo puede ser para un socio o un antiguo socio.',
  recipient_not_allowed: 'Las altas, pérdidas, abandonos y retiradas no tienen destinatario.',

  // Accounting
  account_has_children:
    'Una cuenta que agrupa otras cuentas no puede tener transacciones: usa una de sus cuentas hijas.',
  parent_has_entries:
    'Una cuenta que ya tiene transacciones no puede tener cuentas hijas: elige otra cuenta superior.',
  account_in_use:
    'La cuenta se sigue usando (tiene cuentas hijas o transacciones): no se puede borrar.',
  circular_parent: 'Una cuenta no puede colgar de ella misma ni de una de sus descendientes.',
  duplicate_code: 'Otra cuenta ya tiene ese código.',
  duplicate_account: 'Una cuenta solo puede aparecer una vez.',
  unknown_account: 'Alguna de las cuentas no existe.',
  unknown_parent_account: 'La cuenta superior no existe.',
  invalid_amount: 'Los importes no pueden ser cero.',
  too_few_entries: 'Una transacción necesita al menos dos apuntes.',
  unbalanced: (message) => {
    const m = /income ([\d.,-]+), expenses ([\d.,-]+)/.exec(message ?? '');
    return m
      ? `Los ingresos (${m[1]} €) y los gastos (${m[2]} €) tienen que sumar lo mismo.`
      : 'Los ingresos y los gastos tienen que sumar lo mismo.';
  },
  not_income_for_dues_licenses:
    'Una cuenta para cuotas anuales o licencias tiene que ser de tipo «ingresos».',
  not_income_for_distributions:
    'La cuenta de los pagos de un reparto tiene que ser de tipo «ingresos».',
  not_income_for_material:
    'La cuenta de los alquileres o las ventas de material tiene que ser de tipo «ingresos».',
  no_material_account:
    'No hay ninguna cuenta marcada para los alquileres o las ventas de material (se marca en Contabilidad).',
  transaction_has_material_movement:
    'Esta transacción la registró una venta o un alquiler de material: borra el movimiento en /material y se borrará con él.',
  transaction_has_licenses:
    'Esta transacción es el pago de una o más licencias, así que no se puede borrar.',
  transaction_has_distribution_payments:
    'Esta transacción es el pago de un reparto y ya no se puede deshacer porque los pedidos han cambiado: quita el pago en el reparto.',
  transaction_has_files: 'Esta transacción tiene ficheros adjuntos: quítalos primero.',
  transaction_not_found: 'La transacción no existe.',

  // Budgets
  invalid_item:
    'Cada partida necesita un nombre y un importe de cero o más, con como mucho dos decimales.',
  duplicate_item: withDetail('Hay dos partidas con el mismo nombre en la misma parte'),
  item_without_accounts: withDetail('Cada partida necesita al menos una cuenta'),
  account_not_budgetable: withDetail(
    'Solo las cuentas que tienen transacciones (no las que agrupan otras) pueden estar en un presupuesto',
  ),
  account_type_mismatch: withDetail(
    'El tipo de una cuenta tiene que coincidir con el de la parte donde está la partida',
  ),
  accounts_missing: (message) => {
    const detail = detailOf(message);
    return detail
      ? `Faltan cuentas por incluir en el presupuesto: ${detail}.`
      : 'Faltan cuentas por incluir en el presupuesto.';
  },

  // Club data
  invalid_label: 'Hace falta una etiqueta de hasta 100 caracteres.',
  invalid_value: 'Hace falta un valor de hasta 500 caracteres.',
  invalid_copy:
    'El texto a copiar no puede pasar de 500 caracteres, y el nombre del botón (hasta 100) necesita un texto a copiar.',

  // Distributions
  cutoff_required: 'Un reparto para todos los socios necesita una fecha de corte.',
  invalid_audience: 'El destinatario del reparto no es válido.',
  invalid_price: 'Los precios no pueden ser negativos.',
  invalid_max_extra: 'El máximo de extras necesita un precio de extra y no puede ser negativo.',
  invalid_options:
    'Cada opción necesita un nombre y al menos un valor, y los nombres y los valores no se pueden repetir.',
  invalid_articles:
    'Cada artículo necesita un nombre distinto, un precio de 0 o más y, si tiene límite, uno de al menos 1.',
  articles_conflict:
    'Un reparto con artículos es para quien se apunte y no tiene opciones, extras ni precio propio.',
  invalid_numbering:
    'Cada campo de numeración necesita un nombre distinto y valores como «124-133, 137», con como mucho 100000 en total.',
  invalid_status_transition: 'El estado del reparto solo puede avanzar un paso cada vez.',
  article_required: 'Di qué artículo pides.',
  unknown_article: 'El artículo no existe.',
  invalid_quantity:
    'La cantidad solo vale para unidades de un artículo, y tiene que estar entre 1 y 100000.',
  not_open: 'Este reparto no está abierto para elegir.',
  invalid_choices: 'Hay que elegir exactamente uno de los valores de cada opción.',
  unit_locked: 'Este pedido ya se ha recogido o pagado y no se puede cambiar.',
  unit_paid: 'Este pedido tiene pagos: quítalos primero.',
  not_deliverable: 'Hasta que el reparto deje de ser un borrador no se pueden entregar unidades.',
  no_units: 'Indica al menos una unidad.',
  invalid_collector: 'Quien recoge tiene que ser socio ahora, o haberlo sido en la fecha de corte.',
  automatic_unit:
    'En un reparto para todos los socios cada persona tiene su propia unidad, que no se puede quitar.',
  invalid_identifiers:
    'Este pedido no lleva números, o están vacíos, repetidos o son más que su cantidad.',
  identifiers_missing:
    'Los pedidos de artículos con número necesitan apuntar todos sus números antes de recogerse.',
  has_units: 'Alguien ya ha pedido en este reparto.',
  has_settled_units: 'Hay pedidos ya pagados o recogidos: deshaz antes esas marcas.',
  already_registered: 'Esta persona ya tiene su propia unidad.',
  extras_not_allowed: 'Este reparto no tiene unidades extra.',
  too_many_extras: 'Esta persona ya tiene el máximo de unidades extra.',
  already_picked_up: 'Una de las unidades ya estaba recogida.',
  already_paid: 'Una de las unidades ya estaba marcada como pagada.',
  article_exhausted: 'No quedan unidades suficientes de este artículo.',
  cap_below_requested: 'El límite de un artículo no puede ser menor que lo que ya se ha pedido.',
  article_person_limit: 'Es más de lo que una persona puede tener de este artículo.',
  article_not_numbered: 'Los números de este artículo no están descritos.',
  identifier_taken: 'Otra unidad de este artículo ya tiene ese número.',
  number_in_use: 'Un número que alguien ya tiene dejaría de existir.',
  not_enough_numbers: 'No hay números libres suficientes.',
  identifier_not_assigned: 'Esos números no están asignados a este pedido.',
  identifier_not_paid: 'Esos números no están pagados.',
  identifier_not_delivered: 'Esos números no están entregados.',
  quantity_below_paid: 'La cantidad no puede ser menor que lo ya pagado.',
  quantity_below_numbered:
    'La cantidad no puede ser menor que los números ya asignados: quita antes algunos.',
  quantity_below_delivered: 'La cantidad no puede ser menor que lo ya entregado.',
  account_required: 'Indica la cuenta en la que se hizo el pago.',
  account_not_asset: 'La cuenta en la que se paga tiene que ser de tipo «activo».',
  account_not_income: 'La cuenta de un reparto tiene que ser de tipo «ingresos».',
  no_distribution_account:
    'Este reparto no tiene cuenta para sus pagos y ninguna está marcada como la de los repartos por defecto.',
  unknown_user: 'La persona no existe.',
};

// The Spanish text for an API error code, or undefined when there is none (the caller then keeps the
// API's own message).
function errorText(code, apiMessage) {
  const text = texts[code];
  return typeof text === 'function' ? text(apiMessage) : text;
}

export { errorText };
