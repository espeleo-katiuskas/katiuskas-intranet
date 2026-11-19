// A photo from a phone is several MiB and thousands of pixels wide: far more than a page of the
// intranet needs, and it is slow to send over the little signal there is near a cave (the API takes
// 10 MiB at most). This makes it fit in `maxSide` pixels as a JPEG, rotated as the phone took it
// (the rotation is applied, so the JPEG has none to read). What is not a JPEG, PNG or WebP picture,
// a picture already small, or one that does not get smaller, is returned as it was; so is anything
// the browser cannot draw.
export async function shrinkImage(file, { maxSide = 1600, quality = 0.85 } = {}) {
  if (!/^image\/(jpeg|png|webp)$/.test(file.type) || !window.createImageBitmap) return file;
  try {
    const bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' });
    const scale = Math.min(1, maxSide / Math.max(bitmap.width, bitmap.height));
    if (scale === 1 && file.size <= 1.5 * 1024 * 1024) {
      bitmap.close();
      return file;
    }
    const canvas = document.createElement('canvas');
    canvas.width = Math.round(bitmap.width * scale);
    canvas.height = Math.round(bitmap.height * scale);
    const context = canvas.getContext('2d');
    context.fillStyle = '#fff'; // A transparent PNG would turn black as a JPEG.
    context.fillRect(0, 0, canvas.width, canvas.height);
    context.drawImage(bitmap, 0, 0, canvas.width, canvas.height);
    bitmap.close();
    const blob = await new Promise((resolve) => canvas.toBlob(resolve, 'image/jpeg', quality));
    if (!blob || blob.size >= file.size) return file;
    return new File([blob], `${file.name.replace(/\.[^.]+$/, '')}.jpg`, { type: 'image/jpeg' });
  } catch {
    return file;
  }
}
